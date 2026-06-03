// Package debate provides the debate pipeline builder, persistable state,
// and a Repository abstraction over how that state is stored.
package debate

import (
	"context"
	"errors"
	"fmt"

	"github.com/h-hmz/logos/internal/agent"
	"github.com/h-hmz/logos/internal/philosopher"
)

// step is one node of the debate pipeline: its identity (Key, Label) plus a
// lazy builder so callers that only need metadata never construct an agent.
// The builder is invoked for exactly the one step a turn runs (see runNextTurn).
type step struct {
	Key   string
	Label string
	build func(ctx context.Context) (*agent.Agent, error)
}

// StepMeta is the lightweight form of a pipeline step — no agent, safe to
// derive cheaply from persisted state for handler responses and validation.
type StepMeta struct {
	Key   string
	Label string
}

// StepRecord is the persisted record of one completed pipeline step: its
// content plus the provider-billed token counts and cost for the generation
// that produced it. Records are appended in pipeline order, so element i
// corresponds to BuildStepMeta()[i], and len(StepRecords) is the number of
// completed steps — i.e. the index of the next step to run (see Current).
type StepRecord struct {
	Key          string  `json:"key"           firestore:"key"`
	Content      string  `json:"content"       firestore:"content"`
	InputTokens  int     `json:"input_tokens"  firestore:"input_tokens"`
	OutputTokens int     `json:"output_tokens" firestore:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"      firestore:"cost_usd"`
}

// Persisted is the JSON-serializable state of a debate. It stores debater
// names (not full philosopher.Data) so the catalog can evolve independently.
// StepRecords holds the completed steps (content + per-step tokens and cost),
// used to seed a fresh session on each subsequent request, to derive the
// running token/cost totals, and as the sole source of progress: the count of
// completed steps is len(StepRecords) (see Current). OwnerUID records the
// caller who created the debate; reads stay open (the unguessable ID is the
// capability), but only the owner may advance it (see NextTurn).
type Persisted struct {
	ID           string       `json:"id"                     firestore:"id"`
	OwnerUID     string       `json:"owner_uid"              firestore:"owner_uid"`
	Topic        string       `json:"topic"                  firestore:"topic"`
	DebaterAName string       `json:"debater_a"              firestore:"debater_a"`
	DebaterBName string       `json:"debater_b"              firestore:"debater_b"`
	NumRounds    int          `json:"num_rounds"             firestore:"num_rounds"`
	Models       RoleModels   `json:"models"                 firestore:"models"`
	MaxWords     int          `json:"max_words"              firestore:"max_words"`
	StepRecords  []StepRecord `json:"step_records,omitempty" firestore:"step_records,omitempty"`
	Version      int64        `json:"version"                firestore:"version"`
}

// RoleModels holds the model each pipeline role runs on.
type RoleModels struct {
	DebaterA  string `json:"debater_a" firestore:"debater_a"`
	DebaterB  string `json:"debater_b" firestore:"debater_b"`
	Moderator string `json:"moderator" firestore:"moderator"`
	Critic    string `json:"critic"    firestore:"critic"`
	Judge     string `json:"judge"     firestore:"judge"`
}

// Index of the next pipeline step to run, equal to the number of completed steps.
func (p *Persisted) Current() int { return len(p.StepRecords) }

// Clone returns a deep copy safe to mutate without affecting the original.
func (p *Persisted) Clone() *Persisted {
	cp := *p
	if p.StepRecords != nil {
		cp.StepRecords = make([]StepRecord, len(p.StepRecords))
		copy(cp.StepRecords, p.StepRecords)
	}
	return &cp
}

// TotalInputTokens sums the input tokens across all completed steps.
func (p *Persisted) TotalInputTokens() int {
	var n int
	for i := range p.StepRecords {
		n += p.StepRecords[i].InputTokens
	}
	return n
}

// TotalOutputTokens sums the output tokens across all completed steps.
func (p *Persisted) TotalOutputTokens() int {
	var n int
	for i := range p.StepRecords {
		n += p.StepRecords[i].OutputTokens
	}
	return n
}

// TotalCostUSD sums the provider-billed cost across all completed steps.
func (p *Persisted) TotalCostUSD() float64 {
	var c float64
	for i := range p.StepRecords {
		c += p.StepRecords[i].CostUSD
	}
	return c
}

// StepContent returns the stored content for a step key, if that step has
// completed. The bool reports whether a record for the key exists.
func (p *Persisted) StepContent(key string) (string, bool) {
	for i := range p.StepRecords {
		if p.StepRecords[i].Key == key {
			return p.StepRecords[i].Content, true
		}
	}
	return "", false
}

// Runtime is the per-request working copy of a debate: persisted fields plus
// the non-serializable pieces (resolved debater data and the live state map)
// rebuilt by Hydrate. State carries the per-request scratchpad agents read via
// {key} substitution and write to via OutputKey. The agent for the step being
// run is built on demand from pipeline, not stored here.
type Runtime struct {
	*Persisted
	DebaterA philosopher.Data
	DebaterB philosopher.Data
	State    agent.State
}

// TotalSteps returns the pipeline length for a given round count:
// 1 moderator + 2*N round turns + 1 critic + 2 critic responses + 1 judge.
func TotalSteps(numRounds int) int { return 5 + 2*numRounds }

// BuildStepMeta returns the full (key, label) list without constructing agents.
// Cheap — it enumerates the pipeline but never invokes a step's build closure,
// so the models/maxWords those closures would use are irrelevant here.
func BuildStepMeta(a, b philosopher.Data, numRounds int) []StepMeta {
	steps := pipeline(a, b, numRounds, RoleModels{}, 0)
	out := make([]StepMeta, len(steps))
	for i := range steps {
		out[i] = StepMeta{Key: steps[i].Key, Label: steps[i].Label}
	}
	return out
}

// Hydrate rebuilds a Runtime from a Persisted snapshot: resolves debater data
// via the catalog map and seeds a fresh agent.State map with prior step
// outputs. It builds no agents — the caller constructs only the one step it
// runs (see pipeline). The returned Runtime shares the input *Persisted, so
// handler mutations flow back through repo.Update.
func Hydrate(p *Persisted, philMap map[string]philosopher.Data) (*Runtime, error) {
	a, okA := philMap[p.DebaterAName]
	b, okB := philMap[p.DebaterBName]
	if !okA || !okB {
		return nil, fmt.Errorf("hydrate: unknown philosopher (a=%q ok=%v, b=%q ok=%v)", p.DebaterAName, okA, p.DebaterBName, okB)
	}

	state := agent.State{"topic": p.Topic}
	for i := range p.StepRecords {
		state[p.StepRecords[i].Key] = p.StepRecords[i].Content
	}

	return &Runtime{
		Persisted: p,
		DebaterA:  a,
		DebaterB:  b,
		State:     state,
	}, nil
}

var (
	ErrNotFound        = errors.New("debate not found")
	ErrAlreadyExists   = errors.New("debate already exists")
	ErrVersionConflict = errors.New("debate version conflict")
	// ErrForbidden signals the caller is not the debate's owner and so may not
	// mutate it. Reads are unrestricted, so this only guards the advance path.
	ErrForbidden = errors.New("debate access denied")
)

// pipeline is the single source of truth for the debate's step order. It
// enumerates every step cheaply — no agents are constructed; each step's build
// closure builds its agent only when invoked, so a turn builds exactly one.
// Step i here corresponds 1:1 with StepRecords[i] and BuildStepMeta()[i].
//
// Each step runs on its role's model from models: the debaters' rounds and
// critic-responses on DebaterA/DebaterB, the referee steps on Moderator/Critic/
// Judge. models is expected fully populated (CreateDebate fills unset roles with
// the Default Model), so the pipeline reads it directly with no fallback.
func pipeline(a, b philosopher.Data, numRounds int, models RoleModels, maxWords int) []step {
	var steps []step
	add := func(key, label string, build func(context.Context) (*agent.Agent, error)) {
		steps = append(steps, step{Key: key, Label: label, build: build})
	}

	// Moderator
	add("moderator_intro", "Introduction", func(ctx context.Context) (*agent.Agent, error) {
		return philosopher.MakeModerator(ctx, a, b, models.Moderator)
	})

	// Debate rounds. A speaks first each round (so opens on round 1); B always
	// responds within the same round — hence the asymmetric situation strings
	// and opponent keys.
	for r := 1; r <= numRounds; r++ {
		var bPrevKey string
		if r > 1 {
			bPrevKey = fmt.Sprintf("%s_r%d", philosopher.Key(b.Name), r-1)
		}
		aKey := fmt.Sprintf("%s_r%d", philosopher.Key(a.Name), r)
		bKey := fmt.Sprintf("%s_r%d", philosopher.Key(b.Name), r)

		add(aKey, fmt.Sprintf("%s — Round %d", a.Name, r), func(ctx context.Context) (*agent.Agent, error) {
			return philosopher.MakePhilosopher(ctx, philosopher.MakePhilosopherOpts{
				Data:           a,
				AgentName:      aKey,
				OutputKey:      aKey,
				Situation:      roundSituation(r),
				OpponentArgKey: bPrevKey,
				ModelID:        models.DebaterA,
				MaxWords:       maxWords,
			})
		})
		add(bKey, fmt.Sprintf("%s — Round %d", b.Name, r), func(ctx context.Context) (*agent.Agent, error) {
			return philosopher.MakePhilosopher(ctx, philosopher.MakePhilosopherOpts{
				Data:           b,
				AgentName:      bKey,
				OutputKey:      bKey,
				Situation:      fmt.Sprintf("round %d — respond to your opponent then advance your argument", r),
				OpponentArgKey: aKey,
				ModelID:        models.DebaterB,
				MaxWords:       maxWords,
			})
		})
	}

	// Critic
	add("critic_feedback", "Critic Analysis", func(ctx context.Context) (*agent.Agent, error) {
		return philosopher.MakeCritic(ctx, a, b, numRounds, models.Critic, maxWords*2)
	})

	// Philosopher responses to critic
	aCritKey := fmt.Sprintf("%s_critic_response", philosopher.Key(a.Name))
	add(aCritKey, fmt.Sprintf("%s — Response to Critic", a.Name), func(ctx context.Context) (*agent.Agent, error) {
		return philosopher.MakePhilosopher(ctx, philosopher.MakePhilosopherOpts{
			Data:      a,
			AgentName: fmt.Sprintf("%s_CriticResponse", philosopher.Key(a.Name)),
			OutputKey: aCritKey,
			Situation: "response to the critic — address their challenges and refine your position",
			CriticKey: "critic_feedback",
			ModelID:   models.DebaterA,
			MaxWords:  maxWords,
		})
	})
	bCritKey := fmt.Sprintf("%s_critic_response", philosopher.Key(b.Name))
	add(bCritKey, fmt.Sprintf("%s — Response to Critic", b.Name), func(ctx context.Context) (*agent.Agent, error) {
		return philosopher.MakePhilosopher(ctx, philosopher.MakePhilosopherOpts{
			Data:      b,
			AgentName: fmt.Sprintf("%s_CriticResponse", philosopher.Key(b.Name)),
			OutputKey: bCritKey,
			Situation: "response to the critic — address their challenges and refine your position",
			CriticKey: "critic_feedback",
			ModelID:   models.DebaterB,
			MaxWords:  maxWords,
		})
	})

	// Judge
	add("final_verdict", "Judge's Verdict", func(ctx context.Context) (*agent.Agent, error) {
		return philosopher.MakeJudge(ctx, a, b, numRounds, models.Judge)
	})

	return steps
}

func roundSituation(r int) string {
	if r == 1 {
		return "opening argument — present your core position"
	}
	return fmt.Sprintf("round %d — respond to your opponent then advance your argument", r)
}
