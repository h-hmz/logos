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

## Output Guidance
Output is constrained to a JSON schema by the API — structure is enforced.
Field semantics:
- summary_a.name MUST equal {{printf "%q" .NameA}}; summary_b.name MUST equal {{printf "%q" .NameB}}.
- thesis: 2-3 sentence distillation of the debater's central position.
- clashes: 2-3 items. title is a short label (e.g. "Grounds of moral obligation"); description is 1-2 sentences on the disagreement and how they engaged.
- shifts: 0-3 items; who is one of "A" | "B" | "both" | "neither"; description names the concession, strengthening, or evolution across rounds. Use [] if neither side moved.
- ranking.winner is one of "A" | "B" | "draw"; note is 2-3 sentences on what tipped the scales.
- synthesis: a genuine philosophical position taking the strongest insights from both sides — NOT a diplomatic compromise.
- confidence: integer 1-10 on the scale below; confidence_note is 1-2 sentences justifying the rating.
- open: 2-3 unresolved questions raised by the debate.

Confidence scale:
- 1-3: Irreconcilable positions, no meaningful common ground.
- 4-6: Partial convergence, significant tensions remain.
- 7-9: One position clearly stronger with well-defended arguments.
- 10: Near-decisive (rare in philosophy).
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
		Name:                "Judge",
		Model:               m,
		Instruction:         instruction,
		OutputKey:           "final_verdict",
		ResponseFormat:      judgeSchema,
		MaxCompletionTokens: 16384,
	})
}

// judgeSchema is the strict JSON Schema enforced on the judge's output.
// Strict mode requires every object to set additionalProperties:false and
// list every property in required.
var judgeSchema = &agent.JSONSchema{
	Name: "final_verdict",
	Schema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"summary_a", "summary_b", "clashes", "shifts",
			"ranking", "synthesis", "confidence", "confidence_note", "open",
		},
		"properties": map[string]any{
			"summary_a": debaterSummarySchema,
			"summary_b": debaterSummarySchema,
			"clashes": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"title", "description"},
					"properties": map[string]any{
						"title":       map[string]any{"type": "string"},
						"description": map[string]any{"type": "string"},
					},
				},
			},
			"shifts": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"who", "description"},
					"properties": map[string]any{
						"who": map[string]any{
							"type": "string",
							"enum": []string{"A", "B", "both", "neither"},
						},
						"description": map[string]any{"type": "string"},
					},
				},
			},
			"ranking": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"winner", "note"},
				"properties": map[string]any{
					"winner": map[string]any{
						"type": "string",
						"enum": []string{"A", "B", "draw"},
					},
					"note": map[string]any{"type": "string"},
				},
			},
			"synthesis": map[string]any{"type": "string"},
			"confidence": map[string]any{
				"type":    "integer",
				"minimum": 1,
				"maximum": 10,
			},
			"confidence_note": map[string]any{"type": "string"},
			"open": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	},
}

var debaterSummarySchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"name", "thesis"},
	"properties": map[string]any{
		"name":   map[string]any{"type": "string"},
		"thesis": map[string]any{"type": "string"},
	},
}
