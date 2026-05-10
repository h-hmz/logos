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

// RunResult bundles the outcome of one Run.
type RunResult struct {
	Text  string
	Usage Usage
}

// Run resolves {key} placeholders in the instruction against state,
// calls the model with (system, user) messages, and writes
// state[OutputKey] = text when OutputKey is non-empty.
func (a *Agent) Run(ctx context.Context, state State, userMessage string) (RunResult, error) {
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

	text, usage, err := m.generate(ctx, req)
	if err != nil {
		return RunResult{}, err
	}

	if a.cfg.OutputKey != "" && state != nil {
		state[a.cfg.OutputKey] = text
	}

	return RunResult{Text: text, Usage: usage}, nil
}
