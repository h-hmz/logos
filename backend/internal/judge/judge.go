// Package judge provides a factory for the debate judge agent.
package judge

import (
	"context"
	"fmt"
	"strings"

	"github.com/h-hmz/logos/internal/modelcache"
	"github.com/h-hmz/logos/internal/philosopher"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
)

func fullTranscriptBlock(a, b philosopher.Data, numRounds int) string {
	var lines []string
	for r := 1; r <= numRounds; r++ {
		lines = append(lines, fmt.Sprintf("### Round %d", r))
		lines = append(lines, fmt.Sprintf("**%s (%s):** {%s_r%d}", a.Name, a.School, philosopher.Key(a.Name), r))
		lines = append(lines, fmt.Sprintf("**%s (%s):** {%s_r%d}", b.Name, b.School, philosopher.Key(b.Name), r))
	}

	lines = append(lines, "\n### Critic Intervention\n{critic_feedback}")
	lines = append(lines, "\n### Responses to Critic")
	lines = append(lines, fmt.Sprintf("**%s (%s):** {%s_critic_response}", a.Name, a.School, philosopher.Key(a.Name)))
	lines = append(lines, fmt.Sprintf("**%s (%s):** {%s_critic_response}", b.Name, b.School, philosopher.Key(b.Name)))

	return strings.Join(lines, "\n")
}

// MakeJudge creates the judge agent that synthesizes the full debate into a
// structured JSON verdict. The frontend renders each section of the JSON
// independently (positions, clashes, shifts, ranking, synthesis, confidence,
// open questions), so plain prose is no longer acceptable output.
func MakeJudge(ctx context.Context, a, b philosopher.Data, numRounds int, modelID string) (agent.Agent, error) {
	instruction := fmt.Sprintf(`You are the **Debate Judge**. You have observed a %d-round head-to-head philosophical debate followed by a critic intervention and responses.

## Topic: {topic}

## Full Debate Record
%s

## Output Format — STRICT
Return ONLY a JSON object (no prose, no markdown fences, no preamble). Use this exact schema:

{
  "summary_a": {
    "name": %q,
    "thesis": "<2-3 sentence distillation of their central thesis>"
  },
  "summary_b": {
    "name": %q,
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
`, numRounds, fullTranscriptBlock(a, b, numRounds), a.Name, b.Name)

	m, err := modelcache.Get(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", modelID, err)
	}

	return llmagent.New(llmagent.Config{
		Name:        "Judge",
		Description: "Synthesizes the full debate into a structured JSON verdict.",
		Model:       m,
		Instruction: instruction,
		OutputKey:   "final_verdict",
	})
}
