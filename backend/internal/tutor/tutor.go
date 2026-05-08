// Package tutor provides a factory for the "Explore" AI tutor agent and
// helpers for assembling a grounded, multi-turn tutoring prompt.
//
// The tutor is invoked ad-hoc when a user highlights text in a completed
// philosopher turn. It is ephemeral: no session state is persisted server-side;
// chat history is resent by the client on each follow-up.
package tutor

import (
	"context"
	"fmt"
	"strings"

	"github.com/h-hmz/logos/internal/agent"
)

// Message is one entry in the tutor chat history.
type Message struct {
	Role    string // "user" or "tutor"
	Content string
}

// ExplainInput carries the grounded context + conversation state needed to
// build the user-visible prompt for a single tutor turn.
type ExplainInput struct {
	Topic        string
	Selection    string
	Speaker      string
	Opponent     string
	SpeakerTurn  string
	OpponentTurn string
	History      []Message
}

const instruction = `You are **Noctua**, the owl-companion tutor of a philosophy debate app.

A user has highlighted a passage in a debate and is asking questions about it in a chat with you. You are having a conversation with them — you are NOT answering the same "explain this passage" prompt every turn.

You are warm, precise, and a little bookish. Do not lean on owl metaphors or say "hoot" or narrate your own persona — let the name do that work. Just be a clear, patient tutor who happens to be called Noctua.

How to respond, in order of priority:

1. **Always answer the user's most recent message directly.** That specific message is the question you are answering on this turn. Do not re-explain the highlighted passage unless the user actually asks you to.
2. Use the debate context (topic, speakers, highlighted passage, surrounding turns) as **background** — reach for it when it helps answer the user's question, ignore it when it doesn't.
3. If the user asks a follow-up that builds on your last reply ("give an example", "what about X?", "simpler please"), continue that thread.
4. If the user asks something clearly unrelated to the debate or the passage (e.g. "what should I eat for dinner?"), briefly and warmly say that's outside what you can help with here, and offer to continue exploring the passage. Keep it to one or two sentences — do not silently pivot back to re-explaining the passage.
5. If the user asks how the passage relates to something outside the debate (a scientist, another philosopher, a modern event), try to draw the connection honestly. If no meaningful connection exists, say so.

Style:
- Be a neutral tutor, not a third debater. Do not take a side.
- 2–5 short paragraphs is usually right. Use markdown lists when they genuinely help.
- On the **first** reply, end with one gentle invitation to dig deeper. On later replies, only offer a follow-up when it's natural — do not tack one on every turn.

After your reply, on a new line, append this block with no extra text after it:

<!--suggestions-->
[first follow-up question]
[second follow-up question]
[optional third follow-up question]
<!--/suggestions-->

Each suggestion must be a short question (≤ 10 words) that follows naturally from what you just wrote. Do not include a question the user has already asked. Do not number or bullet them. Always include the opening and closing tags.`

// MakeTutor constructs the tutor LLM agent. The instruction is static; all
// debate-specific grounding is passed through the user message (see
// BuildUserMessage) because this agent is invoked without a debate session.
func MakeTutor(ctx context.Context, modelID string) (*agent.Agent, error) {
	m, err := agent.Get(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", modelID, err)
	}
	return agent.New(agent.Config{
		Name:        "Noctua",
		Model:       m,
		Instruction: instruction,
	})
}

// BuildUserMessage formats the selection, debate grounding, and chat history
// into a single user-role message. The current user question (the last
// History entry with Role=="user") is lifted out and placed at the very end
// as the primary directive so the model responds to it — rather than defaulting
// back to re-explaining the highlighted passage.
func BuildUserMessage(in ExplainInput) string {
	var b strings.Builder

	// Background section. Framed as context, not as the task.
	b.WriteString("## Background context (reference material for the conversation)\n")
	if in.Topic != "" {
		fmt.Fprintf(&b, "- Debate topic: %s\n", in.Topic)
	}
	if in.Speaker != "" {
		fmt.Fprintf(&b, "- Speaker of the highlighted passage: %s\n", in.Speaker)
	}
	if in.Opponent != "" {
		fmt.Fprintf(&b, "- Opposing debater: %s\n", in.Opponent)
	}

	b.WriteString("- Highlighted passage:\n  > ")
	b.WriteString(strings.ReplaceAll(strings.TrimSpace(in.Selection), "\n", "\n  > "))
	b.WriteString("\n")

	if s := strings.TrimSpace(in.SpeakerTurn); s != "" {
		fmt.Fprintf(&b, "\n<turn speaker=%q where-highlight-appears=\"true\">\n%s\n</turn>\n", orDefault(in.Speaker, "speaker"), s)
	}
	if s := strings.TrimSpace(in.OpponentTurn); s != "" {
		fmt.Fprintf(&b, "\n<turn speaker=%q preceding=\"true\">\n%s\n</turn>\n", orDefault(in.Opponent, "opponent"), s)
	}

	// Conversation so far: everything the user has said and the tutor has
	// replied BEFORE the current question. The current user question is
	// lifted out and placed at the end as the actual task.
	prior, current := splitHistory(in.History)

	if len(prior) > 0 {
		b.WriteString("\n## Conversation so far\n")
		for _, m := range prior {
			role := "User"
			if m.Role == "tutor" {
				role = "Tutor"
			}
			fmt.Fprintf(&b, "**%s:** %s\n\n", role, strings.TrimSpace(m.Content))
		}
	}

	b.WriteString("\n---\n\n")
	if current != "" {
		fmt.Fprintf(&b, "The user just said:\n\n> %s\n\nWrite your next reply to the user. Answer that specific message directly, keeping the **highlighted passage** as your primary anchor. Refer to the broader turn context only when it helps illuminate the selection.\n", strings.ReplaceAll(current, "\n", "\n> "))
	} else {
		// No user message yet — this is the initial open of the panel.
		b.WriteString("The user just highlighted the passage above and opened the tutor. Give them a first explanation of the highlighted passage, grounded in the background context. End with one gentle invitation for a follow-up question.\n")
	}

	return b.String()
}

// splitHistory separates the chat log into (everything-before-the-latest-user-message,
// latest-user-message-content). If the history has no user messages, current is "".
func splitHistory(h []Message) (prior []Message, current string) {
	lastUserIdx := -1
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 {
		return h, ""
	}
	return h[:lastUserIdx], strings.TrimSpace(h[lastUserIdx].Content)
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

const suggOpen = "<!--suggestions-->"
const suggClose = "<!--/suggestions-->"

// ParseSuggestions splits content into the visible reply and the follow-up
// suggestion lines embedded in the trailing <!--suggestions--> block. If the
// block is absent the full trimmed content is returned with a nil slice.
func ParseSuggestions(content string) (string, []string) {
	before, after, ok := strings.Cut(content, suggOpen)
	if !ok {
		return strings.TrimSpace(content), nil
	}
	clean := strings.TrimSpace(before)
	rest := after
	end := strings.Index(rest, suggClose)
	if end >= 0 {
		rest = rest[:end]
	}
	var suggestions []string
	for _, line := range strings.Split(rest, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			suggestions = append(suggestions, line)
		}
	}
	return clean, suggestions
}

