package agent

import (
	"context"
	"errors"
	"fmt"
)

// Config configures a single LLM agent (one pipeline step).
type Config struct {
	Name                string
	ModelID             string      // resolved against the OpenAI-compatible client (cached)
	Instruction         string      // may contain {key} placeholders resolved against State
	OutputKey           string      // when non-empty, Run writes the result text to state[OutputKey]
	ResponseFormat      *JSONSchema // when non-nil, constrain model output to this JSON schema
	MaxCompletionTokens int         // when >0, hard cap on generated tokens (cost backstop)
}

// Agent is one bound (instruction, model, output-key) triple. Stateless
// across runs; the per-request scratchpad lives in the State passed
// to Run. Model resolution happens lazily in Run.
type Agent struct {
	cfg Config
}

// New constructs an Agent. The model identified by cfg.ModelID is resolved
// on first Run, not here.
func New(cfg Config) (*Agent, error) {
	if cfg.ModelID == "" {
		return nil, errors.New("agent: empty ModelID")
	}
	return &Agent{cfg: cfg}, nil
}

// Name returns the agent's identifier.
func (a *Agent) Name() string { return a.cfg.Name }

// OutputKey returns the state key the agent writes its output to.
func (a *Agent) OutputKey() string { return a.cfg.OutputKey }

// HasResponseFormat reports whether the agent is constrained to a JSON schema.
// Callers use this to gate token-by-token forwarding: structured outputs are
// delivered to the client as a single payload, not as partial JSON chunks.
func (a *Agent) HasResponseFormat() bool { return a.cfg.ResponseFormat != nil }

// RunResult bundles the outcome of one Run.
type RunResult struct {
	Text    string
	Metrics RunMetrics
}

// RunMetrics is everything measured about one Run: provider-reported token
// counts and cost, plus locally-measured latency in milliseconds. A zero value
// means nothing was reported. Populated cooperatively — the provider call (in
// llm.generate) fills tokens/cost, instrumentedLLM fills latency.
type RunMetrics struct {
	InputTokens    int
	OutputTokens   int
	CostUSD        float64
	TTFTMillis     int
	DurationMillis int
}

// Run resolves {key} placeholders in the instruction against state,
// calls the model with (system, user) messages, and writes
// state[OutputKey] = text when OutputKey is non-empty.
func (a *Agent) Run(ctx context.Context, state State, userMessage string, onToken func(string)) (RunResult, error) {
	m, err := getLLM(ctx, a.cfg.ModelID)
	if err != nil {
		return RunResult{}, fmt.Errorf("agent: get model %s: %w", a.cfg.ModelID, err)
	}

	system, err := substitute(a.cfg.Instruction, state)
	if err != nil {
		return RunResult{}, fmt.Errorf("agent %s: %w", a.cfg.Name, err)
	}
	req := request{
		messages: []message{
			{role: roleSystem, content: system},
			{role: roleUser, content: userMessage},
		},
		responseFormat:      a.cfg.ResponseFormat,
		maxCompletionTokens: a.cfg.MaxCompletionTokens,
	}

	ctx = withAgentName(ctx, a.cfg.Name)

	text, metrics, err := m.generate(ctx, req, onToken)
	if err != nil {
		return RunResult{}, err
	}

	if a.cfg.OutputKey != "" && state != nil {
		state[a.cfg.OutputKey] = text
	}

	return RunResult{Text: text, Metrics: metrics}, nil
}
