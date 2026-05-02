package philosopher

import (
	"context"
	"fmt"
	"strings"

	"github.com/h-hmz/logos/internal/agent"
)

const fallacyTaxonomy = `
### Logical Fallacies to Watch For
- **Ad hominem**: Attacking the person rather than the argument
- **Straw man**: Misrepresenting the opponent's position
- **Circular reasoning**: Using the conclusion as a premise
- **Appeal to authority**: Citing a scholar or thinker *as proof* rather than engaging with their reasoning (e.g. "Ibn Sina said X, therefore X is true")
- **False dichotomy**: Presenting only two options when more exist
- **Equivocation**: Shifting the meaning of a key term mid-argument
- **Non sequitur**: Conclusion doesn't follow from premises
- **Appeal to tradition**: Arguing something is right because it's old

### Special Rule for Theological and Religious Arguments
Citing scripture or divine revelation (e.g. the Quran, the Bible, the Upanishads) is **not** automatically an "Appeal to Authority" fallacy. Within a tradition, these texts function as **foundational epistemic premises** — the starting point of the tradition's reasoning, not a rhetorical shortcut.

Apply the following distinctions:
- **Valid internal move**: A debater cites their own scripture to ground a claim *within* their tradition's framework. Do not flag this as a fallacy. Note it as a faith-based premise.
- **Contested cross-tradition premise**: A debater cites their own scripture *as if it should bind their opponent* who does not share that revelation. Flag this as an **unargued premise** — not a fallacy, but a move that requires either justification on independent grounds or acknowledgement that it only carries weight within the tradition.
- **Still a fallacy**: Citing a *scholar or theologian* (not scripture) as proof without engaging their argument. This remains an Appeal to Authority.
`

func exchangeBlock(a, b Data, numRounds int) string {
	var lines []string
	for r := 1; r <= numRounds; r++ {
		lines = append(lines, fmt.Sprintf("### Round %d", r))
		lines = append(lines, fmt.Sprintf("**%s (%s):** {%s_r%d}", a.Name, a.School, Key(a.Name), r))
		lines = append(lines, fmt.Sprintf("**%s (%s):** {%s_r%d}", b.Name, b.School, Key(b.Name), r))
	}
	return strings.Join(lines, "\n")
}

const criticTemplate = `You are the **Debate Critic**, an expert in logic, argumentation theory, and critical thinking.

You do NOT take sides. Evaluate the *quality of reasoning* across the full debate.

{{.Taxonomy}}

## Topic: {topic}

## Full Debate Transcript
{{.Transcript}}

## Output Format — STRICT
Return ONLY a JSON object (no prose, no markdown fences, no preamble). Use this exact schema:

{
  "intro": "<one-sentence framing of the overall exchange>",
  "debater_a": {
    "name": {{printf "%q" .NameA}},
    "strongest": "<their most compelling point and why>",
    "weakness": "<the most significant flaw or gap>",
    "fallacies": [
      { "name": "<fallacy name from the taxonomy above, or 'unargued premise'>", "instance": "<short quoted or paraphrased instance>" }
    ],
    "challenge": "<one pointed question they must address>"
  },
  "debater_b": {
    "name": {{printf "%q" .NameB}},
    "strongest": "...",
    "weakness": "...",
    "fallacies": [ { "name": "...", "instance": "..." } ],
    "challenge": "..."
  },
  "overall": "<one-paragraph assessment of the debate quality>"
}

Rules:
- Empty fallacies list is allowed — use [].
- Do NOT wrap the JSON in code fences.
- Do NOT prepend or append any commentary.
- Keep the total JSON under roughly {{.MaxWords}} words of content (excluding field names).
`

// MakeCritic creates the critic agent that reviews the full debate. The agent
// emits a structured JSON object (schema below) so the frontend can render
// it as a findings grid rather than free-form prose.
func MakeCritic(ctx context.Context, a, b Data, numRounds int, modelID string, maxWords int) (*agent.Agent, error) {
	instruction, err := render("critic", criticTemplate, struct {
		Taxonomy   string
		Transcript string
		NameA      string
		NameB      string
		MaxWords   int
	}{
		Taxonomy:   fallacyTaxonomy,
		Transcript: exchangeBlock(a, b, numRounds),
		NameA:      a.Name,
		NameB:      b.Name,
		MaxWords:   maxWords,
	})
	if err != nil {
		return nil, err
	}

	m, err := agent.Get(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", modelID, err)
	}

	return agent.New(agent.Config{
		Name:        "Critic",
		Model:       m,
		Instruction: instruction,
		OutputKey:   "critic_feedback",
	})
}
