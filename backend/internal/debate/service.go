package debate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/h-hmz/logos/internal/agent"
	"github.com/h-hmz/logos/internal/models"
	"github.com/h-hmz/logos/internal/philosopher"
	"github.com/h-hmz/logos/internal/quota"
)

// ValidationError signals a bad caller request (malformed/conflicting input).
// Handlers map it to HTTP 400.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func validationErr(msg string) error { return &ValidationError{Msg: msg} }

const InterjectionMaxLen = 1000

// Quota-related sentinel errors. Handlers map ErrOverBudget to HTTP 402 and
// ErrGenerationInFlight to HTTP 409.
var (
	// ErrOverBudget signals the caller has exhausted their lifetime quota.
	ErrOverBudget = errors.New("quota exhausted")
	// ErrGenerationInFlight signals the caller already has a generation
	// executing on this instance; concurrent generations per user are refused.
	ErrGenerationInFlight = errors.New("generation already in flight")
	// ErrProviderAuth signals the upstream LLM provider rejected our API key (401/403)
	ErrProviderAuth = errors.New("llm provider rejected the API key")
	// ErrNotLastStep signals a retry targeted a step that is not the debate's
	// current last record — either an earlier step (only the last is retryable)
	// or a head that has advanced under a stale client. A step key alone can't
	// tell the two apart, and both resolve the same way: refetch and retry.
	// Handlers map it to HTTP 409.
	ErrNotLastStep = errors.New("retry target is not the last step")
)

// Service holds the debate orchestration logic: spawning pipelines, running
// agent turns, and mutating persisted state. It has no HTTP dependencies.
type Service struct {
	repo    Repository
	philMap map[string]philosopher.Data

	// Quota enforcement (ADR 0009), always present — set at construction via
	// NewService. defaultLimitUSD is the ceiling seeded onto a brand-new account;
	// an existing account's own stored limit governs.
	quotaStore      quota.Store
	defaultLimitUSD float64
	inflight        *quota.InFlight
}

// QuotaConfig bundles the per-user quota dependencies, required by every
// Service. DefaultLimitUSD is the ceiling seeded onto a brand-new account.
type QuotaConfig struct {
	Store           quota.Store
	DefaultLimitUSD float64
	InFlight        *quota.InFlight
}

// NewService constructs a Service backed by the given repository and philosopher
// catalog. Quota enforcement is mandatory (ADR 0009) — there is no unmetered
// mode.
func NewService(repo Repository, philMap map[string]philosopher.Data, q QuotaConfig) *Service {
	return &Service{
		repo:            repo,
		philMap:         philMap,
		quotaStore:      q.Store,
		defaultLimitUSD: q.DefaultLimitUSD,
		inflight:        q.InFlight,
	}
}

// admitGeneration enforces the per-user gates that must pass before a
// generation runs: it acquires the in-memory per-UID lock (refusing with
// ErrGenerationInFlight if one is already held) and then checks the budget
// (refusing with ErrOverBudget if spend has reached the limit). The returned
// release function clears the lock and must be deferred by the caller.
//
// Budget reads fail open: if the store can't be read, the generation is allowed
// and the error logged (ADR 0009) — a real store outage already breaks
// generations because debate state lives in the same Firestore.
func (s *Service) admitGeneration(ctx context.Context, uid string) (release func(), err error) {
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
	if addErr := s.quotaStore.Add(ctx, uid, cost, s.defaultLimitUSD); addErr != nil {
		slog.ErrorContext(ctx, "quota: lost deduction (fail-open)",
			"uid", uid, "cost_usd", cost, "error", addErr)
	}
	return nil
}

// ReserveNarration atomically reserves estimateUSD against the caller's budget
// before a narration is served, returning the amount actually reserved (pass it
// to SettleNarration) and ErrOverBudget when the estimate does not fit. A store
// error fails open: the narration is allowed and nothing is reserved (ADR 0013).
// Unlike the generation path this takes no in-flight lock — atomic reservation
// alone bounds concurrent spend.
func (s *Service) ReserveNarration(ctx context.Context, uid string, estimateUSD float64) (reserved float64, err error) {
	ok, rerr := s.quotaStore.Reserve(ctx, uid, estimateUSD, s.defaultLimitUSD)
	if rerr != nil {
		slog.WarnContext(ctx, "quota: narration reserve failed, allowing (fail-open)", "uid", uid, "error", rerr)
		return 0, nil
	}
	if !ok {
		return 0, ErrOverBudget
	}
	return estimateUSD, nil
}

// SettleNarration reconciles a prior reservation to the exact byte-derived cost
// by recording (actualUSD - reserved); a negative delta refunds the unused
// reservation. Recording failures fail open — the cost is already incurred
// (ADR 0013).
func (s *Service) SettleNarration(ctx context.Context, uid string, reserved, actualUSD float64) {
	delta := actualUSD - reserved
	if delta == 0 {
		return
	}
	if err := s.quotaStore.Add(ctx, uid, delta, s.defaultLimitUSD); err != nil {
		slog.ErrorContext(ctx, "quota: narration settle failed (fail-open)", "uid", uid, "delta_usd", delta, "error", err)
	}
}

// QuotaUsage reports a user's current quota standing in USD. For a user with no
// account yet, Limit is the configured seed and Spent is zero; an existing
// account reports its own stored limit. Remaining is clamped at zero so a
// bounded overshoot never surfaces as a negative balance (ADR 0009).
type QuotaUsage struct {
	LimitUSD  float64
	SpentUSD  float64
	Remaining float64
}

// QuotaUsage returns the caller's quota standing for the /me/quota readout. A
// read failure is surfaced as an error (unlike the enforcement path, a readout
// has no generation to fail open for).
func (s *Service) QuotaUsage(ctx context.Context, uid string) (QuotaUsage, error) {
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

	// Records are positional and 1:1 with metas (record i ↔ meta i). len(metas)
	// bounds the loop so a doc with more records than the current round count
	// implies reconstructs only the steps that have metadata, never panicking.
	end := min(len(p.StepRecords), len(metas))
	totalIn, totalOut := p.TotalInputTokens(), p.TotalOutputTokens()
	turns := make(map[int]TurnResult, end)
	for i := range end {
		meta := metas[i]
		role := RoleForStep(meta.Key, a, b)
		turns[i] = TurnResult{
			Step:               meta,
			AgentName:          meta.Key,
			Role:               role,
			Content:            p.StepRecords[i].Content,
			StepIndex:          i,
			TotalSteps:         total,
			Done:               i+1 >= total,
			TurnInputTokens:    p.StepRecords[i].InputTokens,
			TurnOutputTokens:   p.StepRecords[i].OutputTokens,
			TurnCostUSD:        p.StepRecords[i].CostUSD,
			TotalInputTokens:   totalIn,
			TotalOutputTokens:  totalOut,
			TurnTTFTMillis:     p.StepRecords[i].TTFTMillis,
			TurnDurationMillis: p.StepRecords[i].DurationMillis,
			Interjection:       p.StepRecords[i].Interjection,
			Model:              p.StepRecords[i].Model,
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

// validateModel checks a caller-supplied model for a generation. It must be non-empty
// and in the allowlist. Returns the trimmed model or a ValidationError (HTTP 400).
func validateModel(raw string) (string, error) {
	m := strings.TrimSpace(raw)
	if m == "" {
		return "", validationErr("model is required")
	}
	if !models.Allowed(m) {
		return "", validationErr(fmt.Sprintf("model %q is not an allowed model", m))
	}
	return m, nil
}

// CreateDebate validates the request, applies defaults, and persists a new
// debate record owned by uid (the authenticated caller).
func (s *Service) CreateDebate(ctx context.Context, uid string, in CreateDebateParams) (*CreateDebateResult, error) {
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
		OwnerUID:     uid,
		Topic:        in.Topic,
		DebaterAName: a.Name,
		DebaterBName: b.Name,
		NumRounds:    in.NumRounds,
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
	Step               StepMeta
	AgentName          string
	Role               string
	Content            string
	StepIndex          int
	TotalSteps         int
	Done               bool
	TurnInputTokens    int
	TurnOutputTokens   int
	TurnCostUSD        float64
	TotalInputTokens   int
	TotalOutputTokens  int
	TurnTTFTMillis     int
	TurnDurationMillis int
	Interjection       string
	Model              string
}

// NextTurn runs the next pipeline step for the given debate. uid is the
// authenticated caller, charged for the generation when quota is enabled.
// onToken, when non-nil, is invoked for each streamed text delta from the model
// — except for steps whose agent emits structured JSON (critic, judge), where
// partial chunks would expose unparseable JSON to the caller. Pass nil to
// disable streaming entirely.
//
// Only the debate's owner may advance it: a non-owner caller is refused with
// ErrForbidden (checked after the quota gate, so an over-budget caller still
// sees ErrOverBudget first).
//
// Returns ErrNotFound, ErrForbidden, ErrVersionConflict, *ValidationError,
// ErrOverBudget, or ErrGenerationInFlight for callers that need to distinguish
// cases.
func (s *Service) NextTurn(ctx context.Context, uid, debateID, interjection, model string, onToken func(string)) (*TurnResult, error) {
	release, err := s.admitGeneration(ctx, uid)
	if err != nil {
		return nil, err
	}
	defer release()

	var res *TurnResult
	err = s.charge(ctx, uid, func() (float64, error) {
		r, cost, runErr := s.runNextTurn(ctx, uid, debateID, interjection, model, onToken)
		res = r
		return cost, runErr
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// RetryTurn re-runs the debate's last completed step, replacing its record with a fresh generation.
func (s *Service) RetryTurn(ctx context.Context, uid, debateID, stepKey, model string, onToken func(string)) (*TurnResult, error) {
	release, err := s.admitGeneration(ctx, uid)
	if err != nil {
		return nil, err
	}
	defer release()

	var res *TurnResult
	err = s.charge(ctx, uid, func() (float64, error) {
		r, cost, runErr := s.runRetryTurn(ctx, uid, debateID, stepKey, model, onToken)
		res = r
		return cost, runErr
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// runNextTurn loads the debate, enforces the advance preconditions (owner, not
// already complete, an allowlisted model, interjection bounds), and runs the
// next pipeline step. Separated from quota bookkeeping so admitGeneration/charge
// can wrap it uniformly with TutorExplain.
func (s *Service) runNextTurn(ctx context.Context, uid, debateID, interjection, model string, onToken func(string)) (*TurnResult, float64, error) {
	p, err := s.repo.Get(ctx, debateID)
	if err != nil {
		return nil, 0, err
	}
	if p.OwnerUID != uid {
		return nil, 0, ErrForbidden
	}
	if p.Current() >= TotalSteps(p.NumRounds) {
		return nil, 0, validationErr("Debate is already complete")
	}
	mdl, err := validateModel(model)
	if err != nil {
		return nil, 0, err
	}

	// Trim first so whitespace-only input is treated as absent, not a short note.
	interjection = strings.TrimSpace(interjection)
	if len(interjection) > InterjectionMaxLen {
		return nil, 0, validationErr(fmt.Sprintf("interjection exceeds %d characters", InterjectionMaxLen))
	}

	return s.runStep(ctx, p, interjection, mdl, onToken)
}

// runRetryTurn loads the debate, enforces the retry preconditions (owner, a
// completed step exists, stepKey is the current last record, an allowlisted
// model), then re-runs that step on a clone with the last record popped. Working
// on a clone is what makes the retry atomic and non-destructive: the regenerated
// step sees every prior step but not its own old output, and a failed generation
// never reaches repo.Update, so the original record survives. The preserved
// interjection was already validated when the original generation was created.
func (s *Service) runRetryTurn(ctx context.Context, uid, debateID, stepKey, model string, onToken func(string)) (*TurnResult, float64, error) {
	p, err := s.repo.Get(ctx, debateID)
	if err != nil {
		return nil, 0, err
	}
	if p.OwnerUID != uid {
		return nil, 0, ErrForbidden
	}
	if strings.TrimSpace(stepKey) == "" {
		return nil, 0, validationErr("step_key is required")
	}
	if p.Current() == 0 {
		return nil, 0, validationErr("no completed step to retry")
	}
	last := p.StepRecords[p.Current()-1]
	if stepKey != last.Key {
		return nil, 0, ErrNotLastStep
	}
	mdl, err := validateModel(model)
	if err != nil {
		return nil, 0, err
	}

	base := p.Clone()
	base.StepRecords = base.StepRecords[:len(base.StepRecords)-1]
	return s.runStep(ctx, base, last.Interjection, mdl, onToken)
}

// runStep runs the single pipeline step that comes next for base — the step at
// index base.Current() — under the given interjection on the given model,
// appends its record, and commits. base must hold exactly the records completed
// before that step: NextTurn passes the live debate (run the head), RetryTurn
// passes a clone with the last record popped (re-run the head). The append +
// repo.Update is the atomic commit, guarded by base.Version's optimistic check.
func (s *Service) runStep(ctx context.Context, base *Persisted, interjection, model string, onToken func(string)) (*TurnResult, float64, error) {
	stepIndex := base.Current()

	rt, err := Hydrate(base, s.philMap)
	if err != nil {
		return nil, 0, fmt.Errorf("hydrate: %w", err)
	}

	// Build only the one agent this step runs, on the supplied model.
	curStep := pipeline(rt.DebaterA, rt.DebaterB, base.NumRounds, model, base.MaxWords)[stepIndex]
	role := RoleForStep(curStep.Key, rt.DebaterA, rt.DebaterB)

	// An interjection may only steer a debater turn: reject one aimed at a referee
	// step (fail loud, don't silently drop it), then seed the slot it reads. On a
	// retry the note comes from storage and is only ever non-empty on a debater
	// step, so this guard never trips for legitimate data.
	if interjection != "" {
		if role != "a" && role != "b" {
			return nil, 0, validationErr("an interjection can only be added to a debater's turn")
		}
		rt.State[curStep.Key+interjectionKeySuffix] = interjection
	}

	ag, err := curStep.build(ctx, turnInput{Interjection: interjection})
	if err != nil {
		return nil, 0, fmt.Errorf("build step: %w", err)
	}

	cb := onToken
	if ag.HasResponseFormat() {
		cb = nil
	}
	result, err := ag.Run(ctx, rt.State, "continue", cb)
	if err != nil {
		if agent.IsProviderAuthError(err) {
			return nil, 0, fmt.Errorf("%w: %v", ErrProviderAuth, err)
		}
		return nil, 0, fmt.Errorf("agent run: %w", err)
	}
	content := result.Text
	turnInput := result.Metrics.InputTokens
	turnOutput := result.Metrics.OutputTokens
	ttftMS := result.Metrics.TTFTMillis
	durationMS := result.Metrics.DurationMillis

	base.StepRecords = append(base.StepRecords, StepRecord{
		Key:            curStep.Key,
		Content:        content,
		Model:          model,
		Interjection:   interjection,
		InputTokens:    turnInput,
		OutputTokens:   turnOutput,
		CostUSD:        result.Metrics.CostUSD,
		TTFTMillis:     ttftMS,
		DurationMillis: durationMS,
	})

	if err := s.repo.Update(ctx, base); err != nil {
		return nil, 0, err
	}

	return &TurnResult{
		Step:               StepMeta{Key: curStep.Key, Label: curStep.Label},
		AgentName:          ag.Name(),
		Role:               role,
		Content:            content,
		StepIndex:          stepIndex,
		TotalSteps:         TotalSteps(base.NumRounds),
		Done:               base.Current() >= TotalSteps(base.NumRounds),
		TurnInputTokens:    turnInput,
		TurnOutputTokens:   turnOutput,
		TurnCostUSD:        result.Metrics.CostUSD,
		TotalInputTokens:   base.TotalInputTokens(),
		TotalOutputTokens:  base.TotalOutputTokens(),
		TurnTTFTMillis:     ttftMS,
		TurnDurationMillis: durationMS,
		Interjection:       interjection,
		Model:              model,
	}, result.Metrics.CostUSD, nil
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
