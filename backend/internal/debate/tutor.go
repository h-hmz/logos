package debate

import (
	"context"
	"fmt"
	"strings"

	"github.com/h-hmz/logos/internal/agent"
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

// TutorResult holds the clean tutor reply and any follow-up suggestions parsed
// from the embedded <!--suggestions--> block.
type TutorResult struct {
	Content     string
	Suggestions []string
}

// TutorExplain streams a tutor response grounded in the selected turn and its
// immediate predecessor (excluding the moderator). No server-side state is
// persisted; the chat history is resent by the caller on each follow-up.
func (s *Service) TutorExplain(ctx context.Context, debateID string, req TutorRequest, onToken func(string)) (TutorResult, error) {
	selection := strings.TrimSpace(req.Selection)
	if selection == "" {
		return TutorResult{}, validationErr("selection is empty")
	}
	if len(selection) > TutorSelectionMaxLen {
		return TutorResult{}, validationErr(fmt.Sprintf("selection exceeds %d characters", TutorSelectionMaxLen))
	}
	if strings.TrimSpace(req.TurnKey) == "" {
		return TutorResult{}, validationErr("turn_key is required")
	}
	if req.TurnKey == "moderator_intro" {
		return TutorResult{}, validationErr("tutor cannot be used on moderator turns")
	}
	if len(req.History) > TutorHistoryMaxMessages {
		return TutorResult{}, validationErr(fmt.Sprintf("chat history exceeds %d messages", TutorHistoryMaxMessages))
	}
	for _, m := range req.History {
		if m.Role != "user" && m.Role != "tutor" {
			return TutorResult{}, validationErr("history role must be 'user' or 'tutor'")
		}
		if len(m.Content) > TutorMessageMaxLen {
			return TutorResult{}, validationErr(fmt.Sprintf("history message exceeds %d characters", TutorMessageMaxLen))
		}
	}

	p, err := s.repo.Get(ctx, debateID)
	if err != nil {
		return TutorResult{}, err
	}
	a, okA := s.philMap[p.DebaterAName]
	b, okB := s.philMap[p.DebaterBName]
	if !okA || !okB {
		return TutorResult{}, fmt.Errorf("tutor: unknown philosopher in debate")
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
		return TutorResult{}, validationErr("unknown turn_key")
	}

	speakerTurn := strings.TrimSpace(p.StepOutputs[req.TurnKey])
	if speakerTurn == "" {
		return TutorResult{}, validationErr("turn has no content yet")
	}

	speaker := speakerNameForKey(req.TurnKey, a, b)
	opponent := otherDebater(speaker, a, b)
	var opponentTurn string
	for j := idx - 1; j >= 0; j-- {
		if steps[j].Key == "moderator_intro" {
			continue
		}
		if out := strings.TrimSpace(p.StepOutputs[steps[j].Key]); out != "" {
			opponentTurn = out
			opponent = speakerNameForKey(steps[j].Key, a, b)
			break
		}
	}

	tutorAgent, err := tutor.MakeTutor(ctx, p.ModelID)
	if err != nil {
		return TutorResult{}, fmt.Errorf("make tutor: %w", err)
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

	// accumBuf collects all partial tokens for delimiter detection.
	// filteredLen tracks how many bytes have been forwarded via onToken.
	// Once the <!--suggestions--> delimiter is found, streaming stops so
	// the suggestions block never reaches the client as visible text.
	var accumBuf string
	var filteredLen int
	var delimFound bool

	filteredOnToken := func(delta string) {
		if onToken == nil || delimFound {
			return
		}
		accumBuf += delta
		if di := strings.Index(accumBuf, tutor.SuggOpen); di >= 0 {
			delimFound = true
			if di > filteredLen {
				onToken(accumBuf[filteredLen:di])
			}
			return
		}
		// Hold back enough bytes to guard against the delimiter being
		// split across chunk boundaries.
		safeEnd := len(accumBuf) - len(tutor.SuggOpen) + 1
		if safeEnd < 0 {
			safeEnd = 0
		}
		if safeEnd > filteredLen {
			onToken(accumBuf[filteredLen:safeEnd])
			filteredLen = safeEnd
		}
	}

	result, err := tutorAgent.Run(ctx, agent.State{}, userText, filteredOnToken)
	if err != nil {
		return TutorResult{}, fmt.Errorf("tutor: %w", err)
	}

	cleanContent, suggestions := tutor.ParseSuggestions(result.Text)
	return TutorResult{Content: cleanContent, Suggestions: suggestions}, nil
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
