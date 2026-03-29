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

// MakeJudge creates the judge agent that synthesizes the full debate into a verdict.
func MakeJudge(ctx context.Context, a, b philosopher.Data, numRounds int, modelID string) (agent.Agent, error) {
	instruction := fmt.Sprintf(`You are the **Debate Judge**. You have observed a %d-round head-to-head philosophical debate followed by a critic intervention.

## Topic: {topic}

## Full Debate Record
%s

## Your Verdict

### 1. Summary of Core Positions
Distill each philosopher's central thesis in 2-3 sentences.

### 2. Key Points of Clash
Identify the 2-3 moments where the debate was sharpest — where they directly engaged on a fundamental disagreement.

### 3. Did Either Philosopher Shift Their Position?
Note any evolution, concessions, or strengthening of arguments across the rounds.

### 4. Argument Rankings
Who made the stronger case overall, and why? Be specific about what tipped the scales.

### 5. Synthesis
Propose a nuanced answer to the debate topic that takes the strongest insights from both positions.
This should be a genuine philosophical position, not a diplomatic compromise.

### 6. Confidence Assessment
Rate the conclusiveness of this debate 1-10:
- 1-3: Irreconcilable positions, no meaningful common ground
- 4-6: Partial convergence, significant tensions remain
- 7-9: One position clearly stronger with well-defended arguments
- 10: Near-decisive (rare in philosophy)

Explain your rating in 1-2 sentences.

### 7. Open Questions
List 2-3 questions the debate raised but did not resolve.
`, numRounds, fullTranscriptBlock(a, b, numRounds))

	m, err := modelcache.Get(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", modelID, err)
	}

	return llmagent.New(llmagent.Config{
		Name:        "Judge",
		Description: "Synthesizes the full debate into a structured verdict with confidence scoring.",
		Model:       m,
		Instruction: instruction,
		OutputKey:   "final_verdict",
	})
}
