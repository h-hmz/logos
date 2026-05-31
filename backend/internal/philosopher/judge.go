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

Debater A is {{printf "%q" .NameA}}; debater B is {{printf "%q" .NameB}}.

## Topic: {topic}

## Full Debate Record
{{.Transcript}}

## Your Task
You judge the debate *as a whole* — not each debater's reasoning in isolation (the Critic already did that). Say only what a reader of the transcript could not already see for themselves: do NOT re-summarize the positions they just read.

## Output Guidance
Output is constrained to a JSON schema by the API — structure is enforced.
Field semantics:
- scorecard: exactly 4 rows, one per criterion below, in this exact order and using these exact criterion names. For each row, score debater A (score_a) and debater B (score_b) from 1 (poor) to 5 (excellent) as integers, and write a one-sentence note justifying the gap between the two scores.
    1. "Rigor" — soundness and validity of the reasoning; freedom from fallacy.
    2. "Responsiveness" — did they engage the opponent's actual argument and answer the critic's challenge, rather than talk past it?
    3. "Depth" — originality and philosophical insight beyond the obvious.
    4. "Persuasion" — rhetorical force, clarity, and memorability of the case.
  The winner is computed from the column totals by the client — do NOT declare a winner anywhere. Score honestly: a close card (e.g. 14-13) signals a near-tie; a lopsided card signals a decisive result.
- turning_point: the single moment the debate turned. quote MUST be a verbatim excerpt copied word-for-word from the Full Debate Record above — never paraphrase or invent. who is "A" or "B", the speaker of that quote. why is 1-2 sentences on why that moment was decisive.
- ranking.note: 2-3 sentences explaining what the scorecard totals add up to — what tipped the balance, or why it was a draw. Must be consistent with the scores you gave; never praise a side the totals contradict.
- synthesis: a genuine philosophical position taking the strongest insights from both sides — NOT a diplomatic compromise.
- open: 2-3 unresolved questions the debate raised but did not settle.
`

// MakeJudge creates the judge agent that evaluates the full debate as a whole
// and emits a structured JSON verdict. The frontend renders each section of the
// JSON independently (scorecard, turning point, ranking note, synthesis, open
// questions) and derives the winner from the scorecard column totals, so plain
// prose is no longer acceptable output.
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

	return agent.New(agent.Config{
		Name:                "Judge",
		ModelID:             modelID,
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
			"scorecard", "turning_point", "ranking", "synthesis", "open",
		},
		"properties": map[string]any{
			"scorecard": map[string]any{
				"type":  "array",
				"items": scorecardRowSchema,
			},
			"turning_point": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"quote", "who", "why"},
				"properties": map[string]any{
					"quote": map[string]any{"type": "string"},
					"who": map[string]any{
						"type": "string",
						"enum": []string{"A", "B"},
					},
					"why": map[string]any{"type": "string"},
				},
			},
			"ranking": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"note"},
				"properties": map[string]any{
					"note": map[string]any{"type": "string"},
				},
			},
			"synthesis": map[string]any{"type": "string"},
			"open": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	},
}

// scorecardRowSchema is one criterion row: a 1-5 score for each debater plus a
// one-line justification. The client sums score_a/score_b to derive the winner.
var scorecardRowSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"criterion", "score_a", "score_b", "note"},
	"properties": map[string]any{
		"criterion": map[string]any{"type": "string"},
		"score_a":   map[string]any{"type": "integer", "minimum": 1, "maximum": 5},
		"score_b":   map[string]any{"type": "integer", "minimum": 1, "maximum": 5},
		"note":      map[string]any{"type": "string"},
	},
}
