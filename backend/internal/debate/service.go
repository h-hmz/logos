package debate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/philosopher"
	"github.com/h-hmz/logos/internal/quota"
)

// ValidationError signals a bad caller request (malformed/conflicting input).
// Handlers map it to HTTP 400.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func validationErr(msg string) error { return &ValidationError{Msg: msg} }

// Quota-related sentinel errors. Handlers map ErrOverBudget to HTTP 402 and
// ErrGenerationInFlight to HTTP 409.
var (
	// ErrOverBudget signals the caller has exhausted their lifetime quota.
	ErrOverBudget = errors.New("quota exhausted")
	// ErrGenerationInFlight signals the caller already has a generation
	// executing on this instance; concurrent generations per user are refused.
	ErrGenerationInFlight = errors.New("generation already in flight")
)

// Service holds the debate orchestration logic: spawning pipelines, running
// agent turns, and mutating persisted state. It has no HTTP dependencies.
type Service struct {
	repo    Repository
	philMap map[string]philosopher.Data

	// Quota enforcement (ADR 0009). A nil quotaStore means quota is disabled:
	// generations are not metered and defaultLimitUSD/inflight are unused. Set
	// as a unit via WithQuota. defaultLimitUSD is the ceiling seeded onto a
	// brand-new account; an existing account's own stored limit governs.
	quotaStore      quota.Store
	defaultLimitUSD float64
	inflight        *quota.InFlight
}

// NewService constructs a Service backed by the given repository and
// philosopher catalog, with quota enforcement disabled.
func NewService(repo Repository, philMap map[string]philosopher.Data) *Service {
	return &Service{repo: repo, philMap: philMap}
}

// QuotaConfig bundles the dependencies that enable per-user quota enforcement.
// DefaultLimitUSD is the ceiling seeded onto a brand-new account.
type QuotaConfig struct {
	Store           quota.Store
	DefaultLimitUSD float64
	InFlight        *quota.InFlight
}

// WithQuota enables per-user quota enforcement on the service using the given
// dependencies. Call once at construction; returns the same *Service for
// chaining.
func (s *Service) WithQuota(cfg QuotaConfig) *Service {
	s.quotaStore = cfg.Store
	s.defaultLimitUSD = cfg.DefaultLimitUSD
	s.inflight = cfg.InFlight
	return s
}

// admitGeneration enforces the per-user gates that must pass before a
// generation runs: it acquires the in-memory per-UID lock (refusing with
// ErrGenerationInFlight if one is already held) and then checks the budget
// (refusing with ErrOverBudget if spend has reached the limit). The returned
// release function clears the lock and must be deferred by the caller. When
// quota is disabled it is a no-op returning a no-op release.
//
// Budget reads fail open: if the store can't be read, the generation is allowed
// and the error logged (ADR 0009) — a real store outage already breaks
// generations because debate state lives in the same Firestore.
func (s *Service) admitGeneration(ctx context.Context, uid string) (release func(), err error) {
	if s.quotaStore == nil {
		return func() {}, nil
	}
	if !s.inflight.TryAcquire(uid) {
		return func() {}, ErrGenerationInFlight
	}
	release = func() { s.inflight.Release(uid) }

	acct, getErr := s.quotaStore.Get(ctx, uid)
	switch {
	case errors.Is(getErr, quota.ErrNotFound):
		// No account yet: the user's ceiling is the configured seed, which will
		// be snapshotted onto the account when the first generation is charged.
		acct = &quota.Account{UID: uid, SpendLimitUSD: s.defaultLimitUSD}
	case getErr != nil:
		slog.WarnContext(ctx, "quota: spend read failed, allowing (fail-open)",
			"uid", uid, "error", getErr)
		acct = &quota.Account{UID: uid, SpendLimitUSD: s.defaultLimitUSD}
	}
	// An existing account is governed by its own stored limit, not the current
	// config value — changing QUOTA_USD_LIMIT does not move existing users.
	if acct.SpentUSD >= acct.SpendLimitUSD {
		release()
		return func() {}, ErrOverBudget
	}
	return release, nil
}

// charge runs gen and, when quota is enabled and the generation succeeded,
// records its actual provider-billed cost against the caller's spend. The
// configured limit is passed as the seed for a brand-new account (ignored once
// the account exists). Recording failures fail open: the cost is already
// incurred, so a lost deduction is logged rather than surfaced to the caller
// (ADR 0009). Failed generations are not charged (their cost is ~0 anyway).
func (s *Service) charge(ctx context.Context, uid string, gen func() (float64, error)) error {
	cost, err := gen()
	if err != nil {
		return err
	}
	if s.quotaStore == nil {
		return nil
	}
	if addErr := s.quotaStore.Add(ctx, uid, cost, s.defaultLimitUSD); addErr != nil {
		slog.ErrorContext(ctx, "quota: lost deduction (fail-open)",
			"uid", uid, "cost_usd", cost, "error", addErr)
	}
	return nil
}

// QuotaUsage reports a user's current quota standing in USD. Enabled is false
// when quota enforcement is off, in which case the budget figures are
// meaningless and callers should treat the user as unlimited. For a user with
// no account yet, Limit is the configured seed and Spent is zero; an existing
// account reports its own stored limit. Remaining is clamped at zero so a
// bounded overshoot never surfaces as a negative balance (ADR 0009).
type QuotaUsage struct {
	Enabled   bool
	LimitUSD  float64
	SpentUSD  float64
	Remaining float64
}

// QuotaUsage returns the caller's quota standing for the /me/quota readout. A
// read failure is surfaced as an error (unlike the enforcement path, a readout
// has no generation to fail open for).
func (s *Service) QuotaUsage(ctx context.Context, uid string) (QuotaUsage, error) {
	if s.quotaStore == nil {
		return QuotaUsage{Enabled: false}, nil
	}
	limit, spent := s.defaultLimitUSD, 0.0
	acct, err := s.quotaStore.Get(ctx, uid)
	switch {
	case errors.Is(err, quota.ErrNotFound):
		// no account yet: keep seed limit, zero spend
	case err != nil:
		return QuotaUsage{}, err
	default:
		limit, spent = acct.SpendLimitUSD, acct.SpentUSD
	}
	return QuotaUsage{
		Enabled:   true,
		LimitUSD:  limit,
		SpentUSD:  spent,
		Remaining: max(0, limit-spent),
	}, nil
}

// Get returns the persisted debate record by ID. Thin pass-through so handlers
// can read state without holding a Repository reference of their own.
func (s *Service) Get(ctx context.Context, id string) (*Persisted, error) {
	return s.repo.Get(ctx, id)
}

// GetSessionResult bundles everything a client needs to rehydrate a debate
// after losing its local state: the manifest (debaters + step list) and
// reconstructed past turns derived from the persisted step records.
type GetSessionResult struct {
	Debate   *Persisted
	DebaterA philosopher.Data
	DebaterB philosopher.Data
	Steps    []StepMeta
	Turns    map[int]TurnResult
}

// GetSession loads a debate and reconstructs every completed turn from the
// persisted step records. Per-turn token counts are not surfaced here, so
// reconstructed turns carry zeroes for those fields; cumulative totals on
// each turn reflect the latest debate state.
func (s *Service) GetSession(ctx context.Context, id string) (*GetSessionResult, error) {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	a, okA := s.philMap[p.DebaterAName]
	b, okB := s.philMap[p.DebaterBName]
	if !okA || !okB {
		return nil, fmt.Errorf("get session: unknown philosopher in debate %s", id)
	}
	metas := BuildStepMeta(a, b, p.NumRounds)
	total := TotalSteps(p.NumRounds)

	// Records are positional and 1:1 with metas (record i ↔ meta i); bound by
	// len(StepRecords) too so a legacy doc with Current set but no records
	// reconstructs to zero turns rather than panicking on an index.
	end := min(p.Current, len(metas), len(p.StepRecords))
	totalIn, totalOut := p.TotalInputTokens(), p.TotalOutputTokens()
	turns := make(map[int]TurnResult, end)
	for i := range end {
		meta := metas[i]
		turns[i] = TurnResult{
			Step:              meta,
			AgentName:         meta.Key,
			Role:              RoleForStep(meta.Key, a, b),
			Content:           p.StepRecords[i].Content,
			StepIndex:         i,
			TotalSteps:        total,
			Done:              i+1 >= total,
			TotalInputTokens:  totalIn,
			TotalOutputTokens: totalOut,
		}
	}

	return &GetSessionResult{
		Debate:   p,
		DebaterA: a,
		DebaterB: b,
		Steps:    metas,
		Turns:    turns,
	}, nil
}

// ── CreateDebate ───────────────────────────────────────────────────────────

// CreateDebateParams is the caller-supplied input; defaults are applied inside
// the service so every caller sees the same business rules.
type CreateDebateParams struct {
	Topic     string
	DebaterA  string
	DebaterB  string
	NumRounds int
	MaxWords  int
}

// CreateDebateResult bundles the new debate record with the philosopher data
// and step metadata derived from it — everything a caller needs for its
// response without a second catalog lookup.
type CreateDebateResult struct {
	Debate   *Persisted
	DebaterA philosopher.Data
	DebaterB philosopher.Data
	Steps    []StepMeta
}

// CreateDebate validates the request, applies defaults, and persists a new
// debate record.
func (s *Service) CreateDebate(ctx context.Context, in CreateDebateParams) (*CreateDebateResult, error) {
	if in.NumRounds == 0 {
		in.NumRounds = 2
	}
	if in.MaxWords == 0 {
		in.MaxWords = 250
	}

	a, okA := s.philMap[in.DebaterA]
	b, okB := s.philMap[in.DebaterB]
	if !okA || !okB {
		return nil, validationErr("Unknown philosopher name")
	}
	if in.DebaterA == in.DebaterB {
		return nil, validationErr("Debaters must be different")
	}
	if in.NumRounds < 1 || in.NumRounds > 5 {
		return nil, validationErr("num_rounds must be between 1 and 5")
	}
	if in.Topic == "" {
		return nil, validationErr("Topic cannot be empty")
	}

	p := &Persisted{
		ID:           uuid.NewString(),
		Topic:        in.Topic,
		DebaterAName: a.Name,
		DebaterBName: b.Name,
		NumRounds:    in.NumRounds,
		ModelID:      config.DefaultModel(),
		MaxWords:     in.MaxWords,
	}
	if err := s.repo.Create(ctx, p); err != nil {
		return nil, fmt.Errorf("create debate: %w", err)
	}
	return &CreateDebateResult{
		Debate:   p,
		DebaterA: a,
		DebaterB: b,
		Steps:    BuildStepMeta(a, b, in.NumRounds),
	}, nil
}

// ── NextTurn ───────────────────────────────────────────────────────────────

// TurnResult is the domain-level outcome of advancing the debate by one step.
type TurnResult struct {
	Step              StepMeta
	AgentName         string
	Role              string
	Content           string
	StepIndex         int
	TotalSteps        int
	Done              bool
	TurnInputTokens   int
	TurnOutputTokens  int
	TotalInputTokens  int
	TotalOutputTokens int
}

// NextTurn runs the next pipeline step for the given debate. uid is the
// authenticated caller, charged for the generation when quota is enabled.
// onToken, when non-nil, is invoked for each streamed text delta from the model
// — except for steps whose agent emits structured JSON (critic, judge), where
// partial chunks would expose unparseable JSON to the caller. Pass nil to
// disable streaming entirely.
//
// Returns ErrNotFound, ErrVersionConflict, *ValidationError, ErrOverBudget, or
// ErrGenerationInFlight for callers that need to distinguish cases.
func (s *Service) NextTurn(ctx context.Context, uid, debateID string, onToken func(string)) (*TurnResult, error) {
	release, err := s.admitGeneration(ctx, uid)
	if err != nil {
		return nil, err
	}
	defer release()

	var res *TurnResult
	err = s.charge(ctx, uid, func() (float64, error) {
		r, cost, runErr := s.runNextTurn(ctx, debateID, onToken)
		res = r
		return cost, runErr
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// runNextTurn executes one pipeline step and returns the result plus the
// generation's provider-billed cost (Usage.CostUSD). Separated from quota
// bookkeeping so admitGeneration/charge can wrap it uniformly with TutorExplain.
func (s *Service) runNextTurn(ctx context.Context, debateID string, onToken func(string)) (*TurnResult, float64, error) {
	p, err := s.repo.Get(ctx, debateID)
	if err != nil {
		return nil, 0, err
	}
	if p.Current >= TotalSteps(p.NumRounds) {
		return nil, 0, validationErr("Debate is already complete")
	}

	rt, err := Hydrate(ctx, p, s.philMap)
	if err != nil {
		return nil, 0, fmt.Errorf("hydrate: %w", err)
	}
	step := rt.Steps[rt.Current]

	cb := onToken
	if step.Agent.HasResponseFormat() {
		cb = nil
	}
	result, err := step.Agent.Run(ctx, rt.State, "continue", cb)
	if err != nil {
		return nil, 0, fmt.Errorf("agent run: %w", err)
	}
	content := result.Text
	turnInput := result.Usage.InputTokens
	turnOutput := result.Usage.OutputTokens

	p.StepRecords = append(p.StepRecords, StepRecord{
		Key:          step.Key,
		Content:      content,
		InputTokens:  turnInput,
		OutputTokens: turnOutput,
		CostUSD:      result.Usage.CostUSD,
	})
	stepIndex := p.Current
	p.Current++

	if err := s.repo.Update(ctx, p); err != nil {
		return nil, 0, err
	}

	role := RoleForStep(step.Key, rt.DebaterA, rt.DebaterB)

	return &TurnResult{
		Step:              StepMeta{Key: step.Key, Label: step.Label},
		AgentName:         step.Agent.Name(),
		Role:              role,
		Content:           content,
		StepIndex:         stepIndex,
		TotalSteps:        TotalSteps(p.NumRounds),
		Done:              p.Current >= TotalSteps(p.NumRounds),
		TurnInputTokens:   turnInput,
		TurnOutputTokens:  turnOutput,
		TotalInputTokens:  p.TotalInputTokens(),
		TotalOutputTokens: p.TotalOutputTokens(),
	}, result.Usage.CostUSD, nil
}

// ── Shared domain helpers ──────────────────────────────────────────────────

// RoleForStep maps a pipeline step key to the frontend-facing role label.
// Mirrors the derivation in frontend/src/step-role.ts.
func RoleForStep(key string, a, b philosopher.Data) string {
	switch key {
	case "moderator_intro":
		return "moderator"
	case "critic_feedback":
		return "critic"
	case "final_verdict":
		return "judge"
	}
	if strings.HasPrefix(key, philosopher.Key(a.Name)) {
		return "a"
	}
	if strings.HasPrefix(key, philosopher.Key(b.Name)) {
		return "b"
	}
	return "moderator"
}
