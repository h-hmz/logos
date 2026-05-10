package debate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/philosopher"
)

const (
	// InterjectionMaxLen bounds the length of a user-authored interjection.
	InterjectionMaxLen = 800
	// interjectRetryAttempts caps the OCC retry budget for Interject.
	interjectRetryAttempts = 5
)

var roundStepKeyRe = regexp.MustCompile(`_r\d+$`)

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
		var structured any
		if meta.Key == "critic_feedback" || meta.Key == "final_verdict" {
			var parsed any
			if uerr := json.Unmarshal([]byte(content), &parsed); uerr == nil {
				structured = parsed
			}
		}
		turns[i] = TurnResult{
			Step:              meta,
			AgentName:         meta.Key,
			Role:              RoleForStep(meta.Key, a, b),
			Content:           content,
			Structured:        structured,
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
	Structured        any
	StepIndex         int
	TotalSteps        int
	Done              bool
	TurnInputTokens   int
	TurnOutputTokens  int
	TotalInputTokens  int
	TotalOutputTokens int
}

// NextTurn runs the next pipeline step for the given debate.
//
// Returns ErrNotFound, ErrVersionConflict, or *ValidationError for callers
// that need to distinguish cases.
func (s *Service) NextTurn(ctx context.Context, debateID string) (*TurnResult, error) {
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

	result, err := step.Agent.Run(ctx, rt.State, "continue")
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

	// Mark any pending interjection for this gap as consumed by this step.
	for i := range p.Interjections {
		it := &p.Interjections[i]
		if it.BeforeStepIndex == stepIndex && it.ConsumedByKey == "" {
			it.ConsumedByKey = step.Key
			break
		}
	}

	if err := s.repo.Update(ctx, p); err != nil {
		return nil, err
	}

	role := RoleForStep(step.Key, rt.DebaterA, rt.DebaterB)

	var structured any
	if step.Key == "critic_feedback" || step.Key == "final_verdict" {
		var parsed any
		if uerr := json.Unmarshal([]byte(content), &parsed); uerr == nil {
			structured = parsed
		} else {
			slog.ErrorContext(ctx, "failed to parse JSON from step output", "step_key", step.Key, "error", uerr)
		}
	}

	return &TurnResult{
		Step:              StepMeta{Key: step.Key, Label: step.Label},
		AgentName:         step.Agent.Name(),
		Role:              role,
		Content:           content,
		Structured:        structured,
		StepIndex:         stepIndex,
		TotalSteps:        TotalSteps(p.NumRounds),
		Done:              p.Current >= TotalSteps(p.NumRounds),
		TurnInputTokens:   turnInput,
		TurnOutputTokens:  turnOutput,
		TotalInputTokens:  p.TotalInputTokens,
		TotalOutputTokens: p.TotalOutputTokens,
	}, nil
}

// ── Interject ─────────────────────────────────────────────────────────────

// Interject stages a user-authored challenge to be delivered before the next
// debater turn. Retries on version conflict (read-modify-write is cheap).
func (s *Service) Interject(ctx context.Context, debateID, text string) (Interjection, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Interjection{}, validationErr("interjection text is empty")
	}
	if len(text) > InterjectionMaxLen {
		return Interjection{}, validationErr(fmt.Sprintf("interjection exceeds %d characters", InterjectionMaxLen))
	}

	for range interjectRetryAttempts {
		p, err := s.repo.Get(ctx, debateID)
		if err != nil {
			return Interjection{}, err
		}
		a, okA := s.philMap[p.DebaterAName]
		b, okB := s.philMap[p.DebaterBName]
		if !okA || !okB {
			return Interjection{}, fmt.Errorf("interject: unknown philosopher in debate")
		}

		stepsMeta := BuildStepMeta(a, b, p.NumRounds)
		if p.Current >= len(stepsMeta) {
			return Interjection{}, validationErr("Debate is already complete")
		}
		if p.Current == 0 {
			return Interjection{}, validationErr("Cannot interject before the moderator has spoken")
		}
		next := stepsMeta[p.Current]
		role := RoleForStep(next.Key, a, b)
		if (role != "a" && role != "b") || !roundStepKeyRe.MatchString(next.Key) {
			return Interjection{}, validationErr("Interjections are only allowed before a round turn")
		}
		for _, existing := range p.Interjections {
			if existing.BeforeStepIndex == p.Current && existing.ConsumedByKey == "" {
				return Interjection{}, validationErr("An interjection is already pending for this gap")
			}
		}

		it := Interjection{
			Text:            text,
			BeforeStepIndex: p.Current,
		}
		p.Interjections = append(p.Interjections, it)

		if err := s.repo.Update(ctx, p); err == nil {
			return it, nil
		} else if !errors.Is(err, ErrVersionConflict) {
			return Interjection{}, err
		}
	}
	return Interjection{}, ErrVersionConflict
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

// formatInterjection wraps user text into the session-state block the next
// philosopher turn reads via {pending_interjection}. Empty text → empty string.
func formatInterjection(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	return fmt.Sprintf(
		"## Audience Challenge\nA member of the audience has raised the following point:\n\n> %s\n\nAddress this challenge directly and specifically before continuing your argument.",
		strings.ReplaceAll(t, "\n", "\n> "),
	)
}
