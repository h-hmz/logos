// Package debate provides the debate pipeline builder, persistable state,
// and a Repository abstraction over how that state is stored.
package debate

import (
	"context"
	"errors"
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

// HumanDebaterName is the catalog name for the human player entry. The critic
// and judge refer to the human as "The Challenger" in their prose.
const HumanDebaterName = "The Challenger"

// IsHumanDebater reports whether the given philosopher name is the human player.
func IsHumanDebater(name string) bool { return name == HumanDebaterName }

// Step represents a single step in the debate pipeline (live, non-serializable).
type Step struct {
	Key     string
	Label   string
	Agent   agent.Agent // nil when IsHuman == true
	IsHuman bool
}

// StepMeta is the lightweight form of Step — no agent, safe to derive cheaply
// from persisted state for handler responses and validation.
type StepMeta struct {
	Key   string
	Label string
}

// Interjection is a user-authored challenge injected between two pipeline steps.
// It does not advance Current; it is consumed by the step at BeforeStepIndex.
type Interjection struct {
	Text            string `json:"text"                      firestore:"text"`
	BeforeStepIndex int    `json:"before_step_index"         firestore:"before_step_index"`
	ConsumedByKey   string `json:"consumed_by_key,omitempty" firestore:"consumed_by_key,omitempty"`
}

// Persisted is the JSON-serializable state of a debate. It stores debater
// names (not full philosopher.Data) so the catalog can evolve independently.
// StepOutputs holds completed step content keyed by step key; used to seed a
// fresh session on each subsequent request.
type Persisted struct {
	ID                  string            `json:"id"                          firestore:"id"`
	Topic               string            `json:"topic"                       firestore:"topic"`
	DebaterAName        string            `json:"debater_a"                   firestore:"debater_a"`
	DebaterBName        string            `json:"debater_b"                   firestore:"debater_b"`
	NumRounds           int               `json:"num_rounds"                  firestore:"num_rounds"`
	ModelID             string            `json:"model_id"                    firestore:"model_id"`
	MaxWords            int               `json:"max_words"                   firestore:"max_words"`
	Current             int               `json:"current"                     firestore:"current"`
	TotalInputTokens    int               `json:"total_input_tokens"          firestore:"total_input_tokens"`
	TotalOutputTokens   int               `json:"total_output_tokens"         firestore:"total_output_tokens"`
	Interjections       []Interjection    `json:"interjections,omitempty"     firestore:"interjections,omitempty"`
	StepOutputs         map[string]string `json:"step_outputs,omitempty"      firestore:"step_outputs,omitempty"`
	PendingInterjection string            `json:"pending_interjection,omitempty" firestore:"pending_interjection,omitempty"`
	Version             int64             `json:"version"                     firestore:"version"`
}

// Clone returns a deep copy safe to mutate without affecting the original.
func (p *Persisted) Clone() *Persisted {
	cp := *p
	if p.Interjections != nil {
		cp.Interjections = append([]Interjection(nil), p.Interjections...)
	}
	if p.StepOutputs != nil {
		cp.StepOutputs = make(map[string]string, len(p.StepOutputs))
		for k, v := range p.StepOutputs {
			cp.StepOutputs[k] = v
		}
	}
	return &cp
}

// Runtime is the per-request working copy of a debate: persisted fields plus
// the non-serializable pieces (agents, live session) rebuilt by Hydrate.
type Runtime struct {
	*Persisted
	DebaterA       philosopher.Data
	DebaterB       philosopher.Data
	Steps          []Step
	SessionService session.Service
	SessionID      string
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
// via the catalog map, constructs the pipeline agents, and creates a fresh
// in-memory session seeded with prior step outputs and any pending interjection.
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

	state := map[string]any{"topic": p.Topic}
	for k, v := range p.StepOutputs {
		state[k] = v
	}
	state[philosopher.InterjectionStateKey] = p.PendingInterjection

	svc := session.InMemoryService()
	createResp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "logos",
		UserID:  "web",
		State:   state,
	})
	if err != nil {
		return nil, fmt.Errorf("hydrate: create session: %w", err)
	}

	return &Runtime{
		Persisted:      p,
		DebaterA:       a,
		DebaterB:       b,
		Steps:          steps,
		SessionService: svc,
		SessionID:      createResp.Session.ID(),
	}, nil
}

// Repository persists debates. Implementations: MemoryRepo (in-process),
// and later a Firestore-backed version for Cloud Run.
type Repository interface {
	Get(ctx context.Context, id string) (*Persisted, error)
	Create(ctx context.Context, p *Persisted) error
	// Update applies an optimistic write: fails with ErrVersionConflict if the
	// stored Version differs from p.Version. On success, p.Version is advanced
	// to the newly stored version.
	Update(ctx context.Context, p *Persisted) error
}

var (
	ErrNotFound        = errors.New("debate not found")
	ErrAlreadyExists   = errors.New("debate already exists")
	ErrVersionConflict = errors.New("debate version conflict")
)

// MemoryRepo is a process-local Repository for tests and single-instance use.
type MemoryRepo struct {
	mu      sync.RWMutex
	debates map[string]*Persisted
}

func NewMemoryRepo() *MemoryRepo {
	return &MemoryRepo{debates: make(map[string]*Persisted)}
}

func (r *MemoryRepo) Get(_ context.Context, id string) (*Persisted, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.debates[id]
	if !ok {
		return nil, ErrNotFound
	}
	return p.Clone(), nil
}

func (r *MemoryRepo) Create(_ context.Context, p *Persisted) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.debates[p.ID]; exists {
		return ErrAlreadyExists
	}
	cp := p.Clone()
	cp.Version = 1
	r.debates[p.ID] = cp
	p.Version = 1
	return nil
}

func (r *MemoryRepo) Update(_ context.Context, p *Persisted) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.debates[p.ID]
	if !ok {
		return ErrNotFound
	}
	if existing.Version != p.Version {
		return ErrVersionConflict
	}
	cp := p.Clone()
	cp.Version = p.Version + 1
	r.debates[p.ID] = cp
	p.Version = cp.Version
	return nil
}

// BuildSteps constructs the full debate pipeline, mirroring _build_steps() in server.py.
func BuildSteps(ctx context.Context, a, b philosopher.Data, numRounds int, modelID string, maxWords int) ([]Step, error) {
	var steps []Step

	// Moderator
	modModel, err := modelcache.Get(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model for moderator: %w", err)
	}
	modAgent, err := llmagent.New(llmagent.Config{
		Name:        "Moderator",
		Description: "Introduces the head-to-head debate.",
		Model:       modModel,
		Instruction: fmt.Sprintf(`You are the **Debate Moderator** — a ring announcer, not a lecturer.

Tonight: **%s** (%s) vs. **%s** (%s).
Topic: {topic}

Write **one paragraph, 40 words or fewer**. Frame the clash this topic forces between these two — what's actually at stake between them — then hand off to **%s**. That's it.

Do **not**:
- Introduce the debaters' biographies.
- Explain the format, rounds, critic, or judge — the audience already knows.
- Welcome the audience or thank anyone.
- Use generic openers ("Ladies and gentlemen", "Tonight we gather", "Welcome to").

Be theatrical and specific to the topic. One sharp beat, then step aside.`,
			a.Name, a.School, b.Name, b.School, a.Name),
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

		aKey := fmt.Sprintf("%s_r%d", philosopher.Key(a.Name), r)
		if IsHumanDebater(a.Name) {
			steps = append(steps, Step{Key: aKey, Label: fmt.Sprintf("%s — Round %d", a.Name, r), IsHuman: true})
		} else {
			agentA, err := philosopher.Make(ctx, philosopher.MakeOpts{
				Data:                a,
				AgentName:           aKey,
				OutputKey:           aKey,
				Situation:           roundSituation(r),
				OpponentArgKey:      bPrevKey,
				ModelID:             modelID,
				MaxWords:            maxWords,
				IncludeInterjection: true,
			})
			if err != nil {
				return nil, err
			}
			steps = append(steps, Step{Key: aKey, Label: fmt.Sprintf("%s — Round %d", a.Name, r), Agent: agentA})
		}

		bKey := fmt.Sprintf("%s_r%d", philosopher.Key(b.Name), r)
		if IsHumanDebater(b.Name) {
			steps = append(steps, Step{Key: bKey, Label: fmt.Sprintf("%s — Round %d", b.Name, r), IsHuman: true})
		} else {
			agentB, err := philosopher.Make(ctx, philosopher.MakeOpts{
				Data:                b,
				AgentName:           bKey,
				OutputKey:           bKey,
				Situation:           fmt.Sprintf("round %d — respond to your opponent then advance your argument", r),
				OpponentArgKey:      aKey,
				ModelID:             modelID,
				MaxWords:            maxWords,
				IncludeInterjection: true,
			})
			if err != nil {
				return nil, err
			}
			steps = append(steps, Step{Key: bKey, Label: fmt.Sprintf("%s — Round %d", b.Name, r), Agent: agentB})
		}
	}

	// Critic
	criticAgent, err := critic.MakeCritic(ctx, a, b, numRounds, modelID, maxWords*2)
	if err != nil {
		return nil, err
	}
	steps = append(steps, Step{Key: "critic_feedback", Label: "Critic Analysis", Agent: criticAgent})

	// Philosopher / human responses to critic
	aCritKey := fmt.Sprintf("%s_critic_response", philosopher.Key(a.Name))
	if IsHumanDebater(a.Name) {
		steps = append(steps, Step{Key: aCritKey, Label: fmt.Sprintf("%s — Response to Critic", a.Name), IsHuman: true})
	} else {
		aCrit, err := philosopher.Make(ctx, philosopher.MakeOpts{
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
	}

	bCritKey := fmt.Sprintf("%s_critic_response", philosopher.Key(b.Name))
	if IsHumanDebater(b.Name) {
		steps = append(steps, Step{Key: bCritKey, Label: fmt.Sprintf("%s — Response to Critic", b.Name), IsHuman: true})
	} else {
		bCrit, err := philosopher.Make(ctx, philosopher.MakeOpts{
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
	}

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
