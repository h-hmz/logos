package debate

import (
	"context"
	"fmt"
	"strings"

	"github.com/h-hmz/logos/internal/agent"
	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/philosopher"
	"github.com/h-hmz/logos/internal/tutor"
)

const (
	// TutorSelectionMaxLen bounds the highlighted passage length.
	TutorSelectionMaxLen = 2000
	// TutorHistoryMaxMessages bounds the ephemeral chat history length.
	TutorHistoryMaxMessages = 20
	// TutorMessageMaxLen bounds a single chat message length.
	TutorMessageMaxLen = 4000
)

// TutorMessage is one entry in the client-held tutor chat history.
type TutorMessage struct {
	Role    string // "user" or "tutor"
	Content string
}

// TutorRequest is the caller-supplied input to TutorExplain.
type TutorRequest struct {
	Selection string
	TurnKey   string
	History   []TutorMessage
}

// TutorResult holds the clean tutor reply, any follow-up suggestions parsed
// from the embedded <!--suggestions--> block, and the provider-billed cost of
// the generation in USD.
type TutorResult struct {
	Content     string
	Suggestions []string
	CostUSD     float64
}

// TutorExplain returns a tutor response grounded in the selected turn and its
// immediate predecessor (excluding the moderator). uid is the authenticated
// caller, charged for the generation when quota is enabled. No server-side
// state is persisted; the chat history is resent by the caller on each
// follow-up.
//
// Returns ErrNotFound, *ValidationError, ErrOverBudget, or ErrGenerationInFlight
// for callers that need to distinguish cases.
func (s *Service) TutorExplain(ctx context.Context, uid, debateID string, req TutorRequest) (TutorResult, error) {
	release, err := s.admitGeneration(ctx, uid)
	if err != nil {
		return TutorResult{}, err
	}
	defer release()

	var res TutorResult
	err = s.charge(ctx, uid, func() (float64, error) {
		r, cost, runErr := s.runTutorExplain(ctx, debateID, req)
		r.CostUSD = cost
		res = r
		return cost, runErr
	})
	if err != nil {
		return TutorResult{}, err
	}
	return res, nil
}

// runTutorExplain performs the tutor generation and returns the result plus its
// provider-billed cost (Usage.CostUSD). Separated from quota bookkeeping so
// admitGeneration/charge can wrap it uniformly with NextTurn.
func (s *Service) runTutorExplain(ctx context.Context, debateID string, req TutorRequest) (TutorResult, float64, error) {
	selection := strings.TrimSpace(req.Selection)
	if selection == "" {
		return TutorResult{}, 0, validationErr("selection is empty")
	}
	if len(selection) > TutorSelectionMaxLen {
		return TutorResult{}, 0, validationErr(fmt.Sprintf("selection exceeds %d characters", TutorSelectionMaxLen))
	}
	if strings.TrimSpace(req.TurnKey) == "" {
		return TutorResult{}, 0, validationErr("turn_key is required")
	}
	if req.TurnKey == "moderator_intro" {
		return TutorResult{}, 0, validationErr("tutor cannot be used on moderator turns")
	}
	if len(req.History) > TutorHistoryMaxMessages {
		return TutorResult{}, 0, validationErr(fmt.Sprintf("chat history exceeds %d messages", TutorHistoryMaxMessages))
	}
	for _, m := range req.History {
		if m.Role != "user" && m.Role != "tutor" {
			return TutorResult{}, 0, validationErr("history role must be 'user' or 'tutor'")
		}
		if len(m.Content) > TutorMessageMaxLen {
			return TutorResult{}, 0, validationErr(fmt.Sprintf("history message exceeds %d characters", TutorMessageMaxLen))
		}
	}

	p, err := s.repo.Get(ctx, debateID)
	if err != nil {
		return TutorResult{}, 0, err
	}
	a, okA := s.philMap[p.DebaterAName]
	b, okB := s.philMap[p.DebaterBName]
	if !okA || !okB {
		return TutorResult{}, 0, fmt.Errorf("tutor: unknown philosopher in debate")
	}

	steps := BuildStepMeta(a, b, p.NumRounds)
	idx := -1
	for i, st := range steps {
		if st.Key == req.TurnKey {
			idx = i
			break
		}
	}
	if idx < 0 {
		return TutorResult{}, 0, validationErr("unknown turn_key")
	}

	speakerContent, _ := p.StepContent(req.TurnKey)
	speakerTurn := strings.TrimSpace(speakerContent)
	if speakerTurn == "" {
		return TutorResult{}, 0, validationErr("turn has no content yet")
	}

	speaker := speakerNameForKey(req.TurnKey, a, b)
	opponent := otherDebater(speaker, a, b)
	var opponentTurn string
	for j := idx - 1; j >= 0; j-- {
		if steps[j].Key == "moderator_intro" {
			continue
		}
		stepContent, _ := p.StepContent(steps[j].Key)
		if out := strings.TrimSpace(stepContent); out != "" {
			opponentTurn = out
			opponent = speakerNameForKey(steps[j].Key, a, b)
			break
		}
	}

	// The tutor is not a debate-pipeline role; it always runs on the live
	// Default Model rather than anything stored on the debate.
	tutorAgent, err := tutor.MakeTutor(ctx, config.DefaultModel())
	if err != nil {
		return TutorResult{}, 0, fmt.Errorf("make tutor: %w", err)
	}

	userText := tutor.BuildUserMessage(tutor.ExplainInput{
		Topic:        p.Topic,
		Selection:    selection,
		Speaker:      speaker,
		Opponent:     opponent,
		SpeakerTurn:  speakerTurn,
		OpponentTurn: opponentTurn,
		History:      toTutorMessages(req.History),
	})

	result, err := tutorAgent.Run(ctx, agent.State{}, userText, nil)
	if err != nil {
		return TutorResult{}, 0, fmt.Errorf("tutor: %w", err)
	}

	cleanContent, suggestions := tutor.ParseSuggestions(result.Text)
	return TutorResult{Content: cleanContent, Suggestions: suggestions}, result.Usage.CostUSD, nil
}

// speakerNameForKey maps a step key to the human-readable philosopher name
// whose content is stored there. Returns empty for non-philosopher steps.
func speakerNameForKey(key string, a, b philosopher.Data) string {
	if strings.HasPrefix(key, philosopher.Key(a.Name)) {
		return a.Name
	}
	if strings.HasPrefix(key, philosopher.Key(b.Name)) {
		return b.Name
	}
	return ""
}

// otherDebater returns whichever of (a, b) is not `name`. If name is empty or
// matches neither, returns b.Name as a reasonable default.
func otherDebater(name string, a, b philosopher.Data) string {
	if name == a.Name {
		return b.Name
	}
	return a.Name
}

func toTutorMessages(in []TutorMessage) []tutor.Message {
	out := make([]tutor.Message, len(in))
	for i, m := range in {
		out[i] = tutor.Message{Role: m.Role, Content: m.Content}
	}
	return out
}
