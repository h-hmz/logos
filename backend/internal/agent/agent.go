package agent

import (
	"context"
	"errors"
	"fmt"
)

// Config configures a single LLM agent (one pipeline step).
type Config struct {
	Name        string
	Model       LLM
	Instruction string // may contain {key} placeholders resolved against State
	OutputKey   string // when non-empty, Run writes the result text to state[OutputKey]
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
// calls the model with (system, user) messages, streams partial tokens
// through onToken (when non-nil), and writes state[OutputKey] = text
// when OutputKey is non-empty.
//
// Tool calling is not implemented here. If the model returns tool
// calls, Run errors out — they will be handled by a future
// Agent.RunWithTools method (see ADR 0008 §3).
func (a *Agent) Run(
	ctx context.Context,
	state State,
	userMessage string,
	onToken func(string),
) (RunResult, error) {
	system := Substitute(a.cfg.Instruction, state)
	req := Request{
		Model: a.cfg.Model.Name(),
		Messages: []Message{
			{Role: RoleSystem, Content: system},
			{Role: RoleUser, Content: userMessage},
		},
	}

	text, toolCalls, usage, err := a.cfg.Model.Generate(ctx, req, func(ch Chunk) error {
		if ch.Delta != "" && onToken != nil {
			onToken(ch.Delta)
		}
		return nil
	})
	if err != nil {
		return RunResult{}, err
	}
	if len(toolCalls) > 0 {
		return RunResult{}, fmt.Errorf("agent %q: model returned tool calls but Run does not support tools", a.cfg.Name)
	}

	if a.cfg.OutputKey != "" && state != nil {
		state[a.cfg.OutputKey] = text
	}

	return RunResult{Text: text, Usage: usage}, nil
}
