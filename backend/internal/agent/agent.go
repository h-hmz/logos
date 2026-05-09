package agent

import (
	"context"
	"errors"
)

// Config configures a single LLM agent (one pipeline step).
type Config struct {
	Name           string
	Model          LLM
	Instruction    string      // may contain {key} placeholders resolved against State
	OutputKey      string      // when non-empty, Run writes the result text to state[OutputKey]
	ResponseFormat *JSONSchema // when non-nil, constrain model output to this JSON schema
}

// Agent is one bound (instruction, model, output-key) triple. Stateless
// across runs; the per-request scratchpad lives in the State passed
// to Run.
type Agent struct {
	cfg Config
}

// New constructs an Agent. Returns an error if Model is nil.
func New(cfg Config) (*Agent, error) {
	if cfg.Model == nil {
		return nil, errors.New("agent: nil Model")
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
	system := Substitute(a.cfg.Instruction, state)
	req := Request{
		Messages: []Message{
			{Role: RoleSystem, Content: system},
			{Role: RoleUser, Content: userMessage},
		},
		ResponseFormat: a.cfg.ResponseFormat,
	}

	text, usage, err := a.cfg.Model.Generate(ctx, req)
	if err != nil {
		return RunResult{}, err
	}

	if a.cfg.OutputKey != "" && state != nil {
		state[a.cfg.OutputKey] = text
	}

	return RunResult{Text: text, Usage: usage}, nil
}
