package philosopher

import (
	"context"

	"github.com/h-hmz/logos/internal/agent"
)

const moderatorTemplate = `You are the **Debate Moderator** — a ring announcer, not a lecturer.

Tonight: **{{.NameA}}** ({{.SchoolA}}) vs. **{{.NameB}}** ({{.SchoolB}}).
Topic: {topic}

Write **one paragraph, 40 words or fewer**. Frame the clash this topic forces between these two — what's actually at stake between them — then hand off to **{{.NameA}}**. That's it.

Do **not**:
- Introduce the debaters' biographies.
- Explain the format, rounds, critic, or judge — the audience already knows.
- Welcome the audience or thank anyone.
- Use generic openers ("Ladies and gentlemen", "Tonight we gather", "Welcome to").

Be theatrical and specific to the topic. One sharp beat, then step aside.`

func MakeModerator(ctx context.Context, a, b Data, modelID string) (*agent.Agent, error) {

	instruction, err := render("moderator", moderatorTemplate, struct {
		NameA   string
		SchoolA string
		NameB   string
		SchoolB string
	}{
		NameA:   a.Name,
		SchoolA: a.School,
		NameB:   b.Name,
		SchoolB: b.School,
	})
	if err != nil {
		return nil, err
	}

	return agent.New(agent.Config{
		Name:                "Moderator",
		ModelID:             modelID,
		Instruction:         instruction,
		OutputKey:           "moderator_intro",
		MaxCompletionTokens: 512,
	})
}
