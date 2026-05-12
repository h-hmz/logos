// Package debate provides the debate pipeline builder, persistable state,
// and a Repository abstraction over how that state is stored.
package debate

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/h-hmz/logos/internal/agent"
	"github.com/h-hmz/logos/internal/philosopher"
)

// Step represents a single step in the debate pipeline (live, non-serializable).
type Step struct {
	Key   string
	Label string
	Agent *agent.Agent
}

// StepMeta is the lightweight form of Step — no agent, safe to derive cheaply
// from persisted state for handler responses and validation.
type StepMeta struct {
	Key   string
	Label string
}

// Persisted is the JSON-serializable state of a debate. It stores debater
// names (not full philosopher.Data) so the catalog can evolve independently.
// StepOutputs holds completed step content keyed by step key; used to seed a
// fresh session on each subsequent request.
type Persisted struct {
	ID                string            `json:"id"                          firestore:"id"`
	Topic             string            `json:"topic"                       firestore:"topic"`
	DebaterAName      string            `json:"debater_a"                   firestore:"debater_a"`
	DebaterBName      string            `json:"debater_b"                   firestore:"debater_b"`
	NumRounds         int               `json:"num_rounds"                  firestore:"num_rounds"`
	ModelID           string            `json:"model_id"                    firestore:"model_id"`
	MaxWords          int               `json:"max_words"                   firestore:"max_words"`
	Current           int               `json:"current"                     firestore:"current"`
	TotalInputTokens  int               `json:"total_input_tokens"          firestore:"total_input_tokens"`
	TotalOutputTokens int               `json:"total_output_tokens"         firestore:"total_output_tokens"`
	StepOutputs       map[string]string `json:"step_outputs,omitempty"      firestore:"step_outputs,omitempty"`
	Version           int64             `json:"version"                     firestore:"version"`
}

// Clone returns a deep copy safe to mutate without affecting the original.
func (p *Persisted) Clone() *Persisted {
	cp := *p
	if p.StepOutputs != nil {
		cp.StepOutputs = make(map[string]string, len(p.StepOutputs))
		maps.Copy(cp.StepOutputs, p.StepOutputs)
	}
	return &cp
}

// Runtime is the per-request working copy of a debate: persisted fields plus
// the non-serializable pieces (agents, live state map) rebuilt by Hydrate.
// State carries the per-request scratchpad agents read via {key}
// substitution and write to via OutputKey (see ADR 0003).
type Runtime struct {
	*Persisted
	DebaterA philosopher.Data
	DebaterB philosopher.Data
	Steps    []Step
	State    agent.State
}

// TotalSteps returns the pipeline length for a given round count:
// 1 moderator + 2*N round turns + 1 critic + 2 critic responses + 1 judge.
func TotalSteps(numRounds int) int { return 5 + 2*numRounds }

// BuildStepMeta returns the full (key, label) list without constructing
// agents. Cheap — pure string formatting; safe to call on any request.
func BuildStepMeta(a, b philosopher.Data, numRounds int) []StepMeta {
	out := make([]StepMeta, 0, TotalSteps(numRounds))
	out = append(out, StepMeta{Key: "moderator_intro", Label: "Introduction"})
	for r := 1; r <= numRounds; r++ {
		out = append(out, StepMeta{
			Key:   fmt.Sprintf("%s_r%d", philosopher.Key(a.Name), r),
			Label: fmt.Sprintf("%s — Round %d", a.Name, r),
		})
		out = append(out, StepMeta{
			Key:   fmt.Sprintf("%s_r%d", philosopher.Key(b.Name), r),
			Label: fmt.Sprintf("%s — Round %d", b.Name, r),
		})
	}
	out = append(out, StepMeta{Key: "critic_feedback", Label: "Critic Analysis"})
	out = append(out, StepMeta{
		Key:   fmt.Sprintf("%s_critic_response", philosopher.Key(a.Name)),
		Label: fmt.Sprintf("%s — Response to Critic", a.Name),
	})
	out = append(out, StepMeta{
		Key:   fmt.Sprintf("%s_critic_response", philosopher.Key(b.Name)),
		Label: fmt.Sprintf("%s — Response to Critic", b.Name),
	})
	out = append(out, StepMeta{Key: "final_verdict", Label: "Judge's Verdict"})
	return out
}

// Hydrate rebuilds a Runtime from a Persisted snapshot: resolves debater data
// via the catalog map, constructs the pipeline agents, and seeds a fresh
// agent.State map with prior step outputs.
// The returned Runtime shares the input *Persisted — handler mutations flow
// back through repo.Update.
func Hydrate(ctx context.Context, p *Persisted, philMap map[string]philosopher.Data) (*Runtime, error) {
	a, okA := philMap[p.DebaterAName]
	b, okB := philMap[p.DebaterBName]
	if !okA || !okB {
		return nil, fmt.Errorf("hydrate: unknown philosopher (a=%q ok=%v, b=%q ok=%v)", p.DebaterAName, okA, p.DebaterBName, okB)
	}

	steps, err := BuildSteps(ctx, a, b, p.NumRounds, p.ModelID, p.MaxWords)
	if err != nil {
		return nil, fmt.Errorf("hydrate: build steps: %w", err)
	}

	state := agent.State{"topic": p.Topic}
	maps.Copy(state, p.StepOutputs)

	return &Runtime{
		Persisted: p,
		DebaterA:  a,
		DebaterB:  b,
		Steps:     steps,
		State:     state,
	}, nil
}

var (
	ErrNotFound        = errors.New("debate not found")
	ErrAlreadyExists   = errors.New("debate already exists")
	ErrVersionConflict = errors.New("debate version conflict")
)

// BuildSteps constructs the full debate pipeline.
func BuildSteps(ctx context.Context, a, b philosopher.Data, numRounds int, modelID string, maxWords int) ([]Step, error) {
	var steps []Step

	// Moderator
	modAgent, err := philosopher.MakeModerator(ctx, a, b, modelID)
	if err != nil {
		return nil, err
	}
	steps = append(steps, Step{Key: "moderator_intro", Label: "Introduction", Agent: modAgent})

	// Debate rounds
	for r := 1; r <= numRounds; r++ {
		var bPrevKey string
		if r > 1 {
			bPrevKey = fmt.Sprintf("%s_r%d", philosopher.Key(b.Name), r-1)
		}

		aKey := fmt.Sprintf("%s_r%d", philosopher.Key(a.Name), r)
		agentA, err := philosopher.MakePhilosopher(ctx, philosopher.MakePhilosopherOpts{
			Data:           a,
			AgentName:      aKey,
			OutputKey:      aKey,
			Situation:      roundSituation(r),
			OpponentArgKey: bPrevKey,
			ModelID:        modelID,
			MaxWords:       maxWords,
		})
		if err != nil {
			return nil, err
		}
		steps = append(steps, Step{Key: aKey, Label: fmt.Sprintf("%s — Round %d", a.Name, r), Agent: agentA})

		bKey := fmt.Sprintf("%s_r%d", philosopher.Key(b.Name), r)
		agentB, err := philosopher.MakePhilosopher(ctx, philosopher.MakePhilosopherOpts{
			Data:           b,
			AgentName:      bKey,
			OutputKey:      bKey,
			Situation:      fmt.Sprintf("round %d — respond to your opponent then advance your argument", r),
			OpponentArgKey: aKey,
			ModelID:        modelID,
			MaxWords:       maxWords,
		})
		if err != nil {
			return nil, err
		}
		steps = append(steps, Step{Key: bKey, Label: fmt.Sprintf("%s — Round %d", b.Name, r), Agent: agentB})
	}

	// Critic
	criticAgent, err := philosopher.MakeCritic(ctx, a, b, numRounds, modelID, maxWords*2)
	if err != nil {
		return nil, err
	}
	steps = append(steps, Step{Key: "critic_feedback", Label: "Critic Analysis", Agent: criticAgent})

	// Philosopher responses to critic
	aCritKey := fmt.Sprintf("%s_critic_response", philosopher.Key(a.Name))
	aCrit, err := philosopher.MakePhilosopher(ctx, philosopher.MakePhilosopherOpts{
		Data:      a,
		AgentName: fmt.Sprintf("%s_CriticResponse", philosopher.Key(a.Name)),
		OutputKey: aCritKey,
		Situation: "response to the critic — address their challenges and refine your position",
		CriticKey: "critic_feedback",
		ModelID:   modelID,
		MaxWords:  maxWords,
	})
	if err != nil {
		return nil, err
	}
	steps = append(steps, Step{Key: aCritKey, Label: fmt.Sprintf("%s — Response to Critic", a.Name), Agent: aCrit})

	bCritKey := fmt.Sprintf("%s_critic_response", philosopher.Key(b.Name))
	bCrit, err := philosopher.MakePhilosopher(ctx, philosopher.MakePhilosopherOpts{
		Data:      b,
		AgentName: fmt.Sprintf("%s_CriticResponse", philosopher.Key(b.Name)),
		OutputKey: bCritKey,
		Situation: "response to the critic — address their challenges and refine your position",
		CriticKey: "critic_feedback",
		ModelID:   modelID,
		MaxWords:  maxWords,
	})
	if err != nil {
		return nil, err
	}
	steps = append(steps, Step{Key: bCritKey, Label: fmt.Sprintf("%s — Response to Critic", b.Name), Agent: bCrit})

	// Judge
	judgeAgent, err := philosopher.MakeJudge(ctx, a, b, numRounds, modelID)
	if err != nil {
		return nil, err
	}
	steps = append(steps, Step{Key: "final_verdict", Label: "Judge's Verdict", Agent: judgeAgent})

	return steps, nil
}

func roundSituation(r int) string {
	if r == 1 {
		return "opening argument — present your core position"
	}
	return fmt.Sprintf("round %d — respond to your opponent then advance your argument", r)
}
