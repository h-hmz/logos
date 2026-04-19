package debate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"

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

// ── CreateDebate ───────────────────────────────────────────────────────────

// CreateDebateParams is the caller-supplied input; defaults are applied inside
// the service so every caller sees the same business rules.
type CreateDebateParams struct {
	Topic     string
	DebaterA  string
	DebaterB  string
	NumRounds int
	Model     string
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
	if in.Model == "" {
		in.Model = config.DefaultModel()
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
	if !config.ValidModel(in.Model) {
		return nil, validationErr("Unknown model id")
	}

	p := &Persisted{
		ID:           uuid.NewString(),
		Topic:        in.Topic,
		DebaterAName: a.Name,
		DebaterBName: b.Name,
		NumRounds:    in.NumRounds,
		ModelID:      in.Model,
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

// NextTurn runs the next pipeline step for the given debate. If onToken is
// non-nil, partial tokens are streamed through it as they arrive; otherwise
// the agent runs in non-streaming mode.
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

	rn, err := runner.New(runner.Config{
		Agent:          step.Agent,
		AppName:        "logos",
		SessionService: rt.SessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("runner create: %w", err)
	}

	msg := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{genai.NewPartFromText("continue")},
	}

	runCfg := agent.RunConfig{}
	if onToken != nil {
		runCfg.StreamingMode = agent.StreamingModeSSE
	}

	var content string
	var turnInput, turnOutput int
	for event, runErr := range rn.Run(ctx, "web", rt.SessionID, msg, runCfg) {
		if runErr != nil {
			return nil, fmt.Errorf("runner: %w", runErr)
		}
		if event.UsageMetadata != nil {
			if event.UsageMetadata.PromptTokenCount > 0 {
				turnInput = int(event.UsageMetadata.PromptTokenCount)
			}
			if event.UsageMetadata.CandidatesTokenCount > 0 {
				turnOutput = int(event.UsageMetadata.CandidatesTokenCount)
			}
		}
		if event.Content != nil && len(event.Content.Parts) > 0 {
			for _, part := range event.Content.Parts {
				if part.Text != "" {
					if onToken != nil && event.Partial {
						onToken(part.Text)
					}
					content = part.Text
				}
			}
		}
	}

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
			p.PendingInterjection = ""
			break
		}
	}

	if err := s.repo.Update(ctx, p); err != nil {
		return nil, err
	}

	role := RoleForStep(step.Key, rt.DebaterA, rt.DebaterB)

	var structured any
	if step.Key == "critic_feedback" || step.Key == "final_verdict" {
		if raw := extractJSON(content); raw != "" {
			var parsed any
			if uerr := json.Unmarshal([]byte(raw), &parsed); uerr == nil {
				structured = parsed
			} else {
				log.Printf("failed to parse %s JSON: %v", step.Key, uerr)
			}
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

		p.PendingInterjection = formatInterjection(text)
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

// extractJSON strips common wrappers (```json … ``` fences, leading prose)
// and returns the substring from the first '{' to the matching '}'. Returns
// the empty string if no balanced object is found.
func extractJSON(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "```") {
		if nl := strings.IndexByte(t, '\n'); nl >= 0 {
			t = t[nl+1:]
		}
		if end := strings.LastIndex(t, "```"); end >= 0 {
			t = t[:end]
		}
	}
	start := strings.IndexByte(t, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(t); i++ {
		c := t[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return t[start : i+1]
			}
		}
	}
	return ""
}
