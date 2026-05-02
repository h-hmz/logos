package philosopher

import (
	"context"
	"fmt"
	"strings"

	"github.com/h-hmz/logos/internal/agent"
)

// fullTranscriptBlock extends the round-by-round exchange with the critic's
// intervention and each debater's response — the record the judge sees.
func fullTranscriptBlock(a, b Data, numRounds int) string {
	tail := []string{
		"",
		"### Critic Intervention",
		"{critic_feedback}",
		"",
		"### Responses to Critic",
		fmt.Sprintf("**%s (%s):** {%s_critic_response}", a.Name, a.School, Key(a.Name)),
		fmt.Sprintf("**%s (%s):** {%s_critic_response}", b.Name, b.School, Key(b.Name)),
	}
	return exchangeBlock(a, b, numRounds) + "\n" + strings.Join(tail, "\n")
}

const judgeTemplate = `You are the **Debate Judge**. You have observed a {{.NumRounds}}-round head-to-head philosophical debate followed by a critic intervention and responses.

## Topic: {topic}

## Full Debate Record
{{.Transcript}}

## Output Format — STRICT
Return ONLY a JSON object (no prose, no markdown fences, no preamble). Use this exact schema:

{
  "summary_a": {
    "name": {{printf "%q" .NameA}},
    "thesis": "<2-3 sentence distillation of their central thesis>"
  },
  "summary_b": {
    "name": {{printf "%q" .NameB}},
    "thesis": "<2-3 sentence distillation of their central thesis>"
  },
  "clashes": [
    {
      "title": "<short label, e.g. 'Grounds of moral obligation'>",
      "description": "<1-2 sentences on the specific disagreement and how they engaged>"
    }
  ],
  "shifts": [
    {
      "who": "A" | "B" | "both" | "neither",
      "description": "<concession, strengthening, or evolution across rounds>"
    }
  ],
  "ranking": {
    "winner": "A" | "B" | "draw",
    "note": "<why — what tipped the scales, in 2-3 sentences>"
  },
  "synthesis": "<a genuine philosophical position that takes the strongest insights from both sides — NOT a diplomatic compromise>",
  "confidence": <integer 1-10>,
  "confidence_note": "<1-2 sentences justifying the rating>",
  "open": [
    "<one of 2-3 questions the debate raised but did not resolve>"
  ]
}

Confidence scale:
- 1-3: Irreconcilable positions, no meaningful common ground.
- 4-6: Partial convergence, significant tensions remain.
- 7-9: One position clearly stronger with well-defended arguments.
- 10: Near-decisive (rare in philosophy).

Rules:
- clashes: 2-3 items.
- shifts: 0-3 items; use [] if neither side moved.
- open: 2-3 items.
- Do NOT wrap the JSON in code fences.
- Do NOT prepend or append any commentary.
`

// MakeJudge creates the judge agent that synthesizes the full debate into a
// structured JSON verdict. The frontend renders each section of the JSON
// independently (positions, clashes, shifts, ranking, synthesis, confidence,
// open questions), so plain prose is no longer acceptable output.
func MakeJudge(ctx context.Context, a, b Data, numRounds int, modelID string) (*agent.Agent, error) {
	instruction, err := render("judge", judgeTemplate, struct {
		NumRounds  int
		Transcript string
		NameA      string
		NameB      string
	}{
		NumRounds:  numRounds,
		Transcript: fullTranscriptBlock(a, b, numRounds),
		NameA:      a.Name,
		NameB:      b.Name,
	})
	if err != nil {
		return nil, err
	}

	m, err := agent.Get(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", modelID, err)
	}

	return agent.New(agent.Config{
		Name:        "Judge",
		Model:       m,
		Instruction: instruction,
		OutputKey:   "final_verdict",
	})
}
