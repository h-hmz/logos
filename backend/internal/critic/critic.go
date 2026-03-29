// Package critic provides a factory for the debate critic agent.
package critic

import (
	"context"
	"fmt"
	"strings"

	"github.com/h-hmz/logos/internal/modelcache"
	"github.com/h-hmz/logos/internal/philosopher"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
)

const FallacyTaxonomy = `
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

func exchangeBlock(a, b philosopher.Data, numRounds int) string {
	var lines []string
	for r := 1; r <= numRounds; r++ {
		lines = append(lines, fmt.Sprintf("### Round %d", r))
		lines = append(lines, fmt.Sprintf("**%s (%s):** {%s_r%d}", a.Name, a.School, philosopher.Key(a.Name), r))
		lines = append(lines, fmt.Sprintf("**%s (%s):** {%s_r%d}", b.Name, b.School, philosopher.Key(b.Name), r))
	}
	return strings.Join(lines, "\n")
}

// MakeCritic creates the critic agent that reviews the full debate.
func MakeCritic(ctx context.Context, a, b philosopher.Data, numRounds int, modelID string, maxWords int) (agent.Agent, error) {
	instruction := fmt.Sprintf(`You are the **Debate Critic**, an expert in logic, argumentation theory, and critical thinking.

You do NOT take sides. Evaluate the *quality of reasoning* across the full debate.

%s

## Topic: {topic}

## Full Debate Transcript
%s

## Your Analysis

For **%s (%s)**:
1. **Strongest argument**: Which of their points was most logically compelling and why?
2. **Key weakness**: The most significant flaw or gap across their arguments.
3. **Fallacies detected**: List any logical fallacies with the specific instance.
4. **Challenge**: One pointed question they must address.

For **%s (%s)**:
1. **Strongest argument**: Which of their points was most logically compelling and why?
2. **Key weakness**: The most significant flaw or gap across their arguments.
3. **Fallacies detected**: List any logical fallacies with the specific instance.
4. **Challenge**: One pointed question they must address.

End with a one-paragraph overall assessment of the debate quality.
Keep your response under %d words.
`, FallacyTaxonomy, exchangeBlock(a, b, numRounds),
		a.Name, a.School, b.Name, b.School, maxWords)

	m, err := modelcache.Get(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", modelID, err)
	}

	return llmagent.New(llmagent.Config{
		Name:        "Critic",
		Description: "Reviews the full debate for logical fallacies and reasoning quality.",
		Model:       m,
		Instruction: instruction,
		OutputKey:   "critic_feedback",
	})
}
