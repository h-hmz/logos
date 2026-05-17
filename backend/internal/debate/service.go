package debate

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/philosopher"
)

// ValidationError signals a bad caller request (malformed/conflicting input).
// Handlers map it to HTTP 400.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func validationErr(msg string) error { return &ValidationError{Msg: msg} }

// Service holds the debate orchestration logic: spawning pipelines, running
// agent turns, and mutating persisted state. It has no HTTP dependencies.
type Service struct {
	repo    Repository
	philMap map[string]philosopher.Data
}

// NewService constructs a Service backed by the given repository and
// philosopher catalog.
func NewService(repo Repository, philMap map[string]philosopher.Data) *Service {
	return &Service{repo: repo, philMap: philMap}
}

// Get returns the persisted debate record by ID. Thin pass-through so handlers
// can read state without holding a Repository reference of their own.
func (s *Service) Get(ctx context.Context, id string) (*Persisted, error) {
	return s.repo.Get(ctx, id)
}

// GetSessionResult bundles everything a client needs to rehydrate a debate
// after losing its local state: the manifest (debaters + step list) and
// reconstructed past turns derived from StepOutputs.
type GetSessionResult struct {
	Debate   *Persisted
	DebaterA philosopher.Data
	DebaterB philosopher.Data
	Steps    []StepMeta
	Turns    map[int]TurnResult
}

// GetSession loads a debate and reconstructs every completed turn from the
// persisted step outputs. Per-turn token counts are not retained on Persisted,
// so reconstructed turns carry zeroes for those fields; cumulative totals on
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

	end := min(p.Current, len(metas))
	turns := make(map[int]TurnResult, end)
	for i := range end {
		meta := metas[i]
		content, ok := p.StepOutputs[meta.Key]
		if !ok {
			continue
		}
		turns[i] = TurnResult{
			Step:              meta,
			AgentName:         meta.Key,
			Role:              RoleForStep(meta.Key, a, b),
			Content:           content,
			StepIndex:         i,
			TotalSteps:        total,
			Done:              i+1 >= total,
			TotalInputTokens:  p.TotalInputTokens,
			TotalOutputTokens: p.TotalOutputTokens,
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
		StepOutputs:  map[string]string{},
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

// NextTurn runs the next pipeline step for the given debate. onToken, when
// non-nil, is invoked for each streamed text delta from the model — except for
// steps whose agent emits structured JSON (critic, judge), where partial
// chunks would expose unparseable JSON to the caller. Pass nil to disable
// streaming entirely.
//
// Returns ErrNotFound, ErrVersionConflict, or *ValidationError for callers
// that need to distinguish cases.
func (s *Service) NextTurn(ctx context.Context, debateID string, onToken func(string)) (*TurnResult, error) {
	p, err := s.repo.Get(ctx, debateID)
	if err != nil {
		return nil, err
	}
	if p.Current >= TotalSteps(p.NumRounds) {
		return nil, validationErr("Debate is already complete")
	}

	rt, err := Hydrate(ctx, p, s.philMap)
	if err != nil {
		return nil, fmt.Errorf("hydrate: %w", err)
	}
	step := rt.Steps[rt.Current]

	cb := onToken
	if step.Agent.HasResponseFormat() {
		cb = nil
	}
	result, err := step.Agent.Run(ctx, rt.State, "continue", cb)
	if err != nil {
		return nil, fmt.Errorf("agent run: %w", err)
	}
	content := result.Text
	turnInput := result.Usage.InputTokens
	turnOutput := result.Usage.OutputTokens

	if p.StepOutputs == nil {
		p.StepOutputs = map[string]string{}
	}
	p.StepOutputs[step.Key] = content
	p.TotalInputTokens += turnInput
	p.TotalOutputTokens += turnOutput
	stepIndex := p.Current
	p.Current++

	if err := s.repo.Update(ctx, p); err != nil {
		return nil, err
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
		TotalInputTokens:  p.TotalInputTokens,
		TotalOutputTokens: p.TotalOutputTokens,
	}, nil
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
