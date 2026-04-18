// Package debate provides the debate pipeline builder, state management,
// and an in-memory store for active debates.
package debate

import (
	"context"
	"fmt"
	"sync"

	"github.com/h-hmz/logos/internal/critic"
	"github.com/h-hmz/logos/internal/judge"
	"github.com/h-hmz/logos/internal/modelcache"
	"github.com/h-hmz/logos/internal/philosopher"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/session"
)

// Step represents a single step in the debate pipeline.
type Step struct {
	Key   string
	Label string
	Agent agent.Agent
}

// State holds the full context for an active debate.
type State struct {
	ID                string
	Topic             string
	DebaterA          philosopher.Data
	DebaterB          philosopher.Data
	Steps             []Step
	SessionService    session.Service
	SessionID         string
	Current           int
	TotalInputTokens  int
	TotalOutputTokens int
	Interjections     []Interjection

	Mu sync.Mutex // serializes /next and /interject calls within a debate
}

// Interjection is a user-authored challenge injected between two pipeline steps.
// It does not advance Current; it is consumed by the step at BeforeStepIndex.
type Interjection struct {
	Text            string `json:"text"`
	BeforeStepIndex int    `json:"before_step_index"`
	ConsumedByKey   string `json:"consumed_by_key,omitempty"`
}

// Store is a thread-safe in-memory store for active debates.
type Store struct {
	mu      sync.RWMutex
	debates map[string]*State
}

func NewStore() *Store {
	return &Store{debates: make(map[string]*State)}
}

func (s *Store) Get(id string) (*State, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.debates[id]
	return d, ok
}

func (s *Store) Put(id string, d *State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.debates[id] = d
}

// BuildSteps constructs the full debate pipeline, mirroring _build_steps() in server.py.
func BuildSteps(ctx context.Context, a, b philosopher.Data, numRounds int, modelID string, maxWords int) ([]Step, error) {
	var steps []Step

	// Moderator
	modModel, err := modelcache.Get(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model for moderator: %w", err)
	}
	modWords := int(float64(maxWords) * 0.8)
	modAgent, err := llmagent.New(llmagent.Config{
		Name:        "Moderator",
		Description: "Introduces the head-to-head debate.",
		Model:       modModel,
		Instruction: fmt.Sprintf(`You are the **Debate Moderator**.

Today's debate is a head-to-head between:
- **%s** representing **%s**
- **%s** representing **%s**

The topic is: {topic}

1. Welcome the audience and state the topic with gravitas.
2. Briefly introduce each debater — who they represent and their core stance.
3. Explain the format: %d round(s) of back-and-forth exchange, followed by a critic intervention, each philosopher's response to the critic, and a final judge's verdict.
4. Announce that **%s** will open the debate.

Keep under %d words. Be theatrical — this is a clash of worldviews.`,
			a.Name, a.School, b.Name, b.School,
			numRounds, a.Name, modWords),
		OutputKey: "moderator_intro",
	})
	if err != nil {
		return nil, fmt.Errorf("create moderator: %w", err)
	}
	steps = append(steps, Step{Key: "moderator_intro", Label: "Introduction", Agent: modAgent})

	// Debate rounds
	for r := 1; r <= numRounds; r++ {
		var bPrevKey string
		if r > 1 {
			bPrevKey = fmt.Sprintf("%s_r%d", philosopher.Key(b.Name), r-1)
		}

		agentA, err := philosopher.Make(ctx, philosopher.MakeOpts{
			Data:                a,
			AgentName:           fmt.Sprintf("%s_r%d", philosopher.Key(a.Name), r),
			OutputKey:           fmt.Sprintf("%s_r%d", philosopher.Key(a.Name), r),
			Situation:           roundSituation(r),
			OpponentArgKey:      bPrevKey,
			ModelID:             modelID,
			MaxWords:            maxWords,
			IncludeInterjection: true,
		})
		if err != nil {
			return nil, err
		}
		steps = append(steps, Step{
			Key:   fmt.Sprintf("%s_r%d", philosopher.Key(a.Name), r),
			Label: fmt.Sprintf("%s — Round %d", a.Name, r),
			Agent: agentA,
		})

		agentB, err := philosopher.Make(ctx, philosopher.MakeOpts{
			Data:                b,
			AgentName:           fmt.Sprintf("%s_r%d", philosopher.Key(b.Name), r),
			OutputKey:           fmt.Sprintf("%s_r%d", philosopher.Key(b.Name), r),
			Situation:           fmt.Sprintf("round %d — respond to your opponent then advance your argument", r),
			OpponentArgKey:      fmt.Sprintf("%s_r%d", philosopher.Key(a.Name), r),
			ModelID:             modelID,
			MaxWords:            maxWords,
			IncludeInterjection: true,
		})
		if err != nil {
			return nil, err
		}
		steps = append(steps, Step{
			Key:   fmt.Sprintf("%s_r%d", philosopher.Key(b.Name), r),
			Label: fmt.Sprintf("%s — Round %d", b.Name, r),
			Agent: agentB,
		})
	}

	// Critic
	criticAgent, err := critic.MakeCritic(ctx, a, b, numRounds, modelID, maxWords*2)
	if err != nil {
		return nil, err
	}
	steps = append(steps, Step{Key: "critic_feedback", Label: "Critic Analysis", Agent: criticAgent})

	// Philosopher responses to critic
	aCrit, err := philosopher.Make(ctx, philosopher.MakeOpts{
		Data:      a,
		AgentName: fmt.Sprintf("%s_CriticResponse", philosopher.Key(a.Name)),
		OutputKey: fmt.Sprintf("%s_critic_response", philosopher.Key(a.Name)),
		Situation: "response to the critic — address their challenges and refine your position",
		CriticKey: "critic_feedback",
		ModelID:   modelID,
		MaxWords:  maxWords,
	})
	if err != nil {
		return nil, err
	}
	steps = append(steps, Step{
		Key:   fmt.Sprintf("%s_critic_response", philosopher.Key(a.Name)),
		Label: fmt.Sprintf("%s — Response to Critic", a.Name),
		Agent: aCrit,
	})

	bCrit, err := philosopher.Make(ctx, philosopher.MakeOpts{
		Data:      b,
		AgentName: fmt.Sprintf("%s_CriticResponse", philosopher.Key(b.Name)),
		OutputKey: fmt.Sprintf("%s_critic_response", philosopher.Key(b.Name)),
		Situation: "response to the critic — address their challenges and refine your position",
		CriticKey: "critic_feedback",
		ModelID:   modelID,
		MaxWords:  maxWords,
	})
	if err != nil {
		return nil, err
	}
	steps = append(steps, Step{
		Key:   fmt.Sprintf("%s_critic_response", philosopher.Key(b.Name)),
		Label: fmt.Sprintf("%s — Response to Critic", b.Name),
		Agent: bCrit,
	})

	// Judge
	judgeAgent, err := judge.MakeJudge(ctx, a, b, numRounds, modelID)
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
