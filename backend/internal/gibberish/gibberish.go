// Package gibberish provides a local agent.LLM that generates random
// philosophical-sounding nonsense — no network calls required. Used
// as the default when MODEL=local-gibberish (or when no MODEL env var
// is set). Side-effect import: blank-importing this package triggers
// the init() that registers the mock with internal/agent.
package gibberish

import (
	"context"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"

	"github.com/h-hmz/logos/internal/agent"
)

const ModelID = "local-gibberish"

var wordLimitRe = regexp.MustCompile(`under (\d+) words`)

var wordList = []string{
	"wherefore", "ontological", "epistemological", "dialectical", "phenomenological",
	"hermeneutical", "noumenal", "categorical", "apriori", "transcendental",
	"empirical", "metaphysical", "axiological", "teleological", "deontological",
	"consequentialist", "utilitarian", "existential", "subliminal", "paradoxical",
	"antinomical", "platonic", "aristotelian", "kantian", "hegelian",
	"nietzschean", "cartesian", "socratic", "stoic", "peripatetic",
	"essence", "substance", "logos", "telos", "ethos",
	"pathos", "nous", "pneuma", "flux", "being",
	"nothingness", "will", "reason", "duty", "virtue",
	"pleasure", "pain", "truth", "beauty", "justice",
}

// Model is the gibberish LLM. Stateless and goroutine-safe.
type Model struct{}

func init() {
	agent.RegisterFactory(ModelID, func() agent.LLM { return &Model{} })
}

func (m *Model) Name() string { return ModelID }

// Generate emits the gibberish text in two halves so SSE streaming is
// observable in local dev. UsageMetadata is approximated by char/4.
func (m *Model) Generate(
	_ context.Context,
	req agent.Request,
	onChunk func(agent.Chunk) error,
) (string, []agent.ToolCall, agent.Usage, error) {
	systemText := ""
	inputChars := 0
	for _, msg := range req.Messages {
		inputChars += len(msg.Content)
		if msg.Role == agent.RoleSystem {
			systemText = msg.Content
		}
	}
	text := generate(wordLimitFrom(systemText))
	usage := agent.Usage{
		InputTokens:  inputChars / 4,
		OutputTokens: len(text) / 4,
	}

	if onChunk != nil {
		mid := len(text) / 2
		if err := onChunk(agent.Chunk{Delta: text[:mid]}); err != nil {
			return "", nil, agent.Usage{}, err
		}
		if err := onChunk(agent.Chunk{Delta: text[mid:]}); err != nil {
			return "", nil, agent.Usage{}, err
		}
	}
	return text, nil, usage, nil
}

// wordLimitFrom extracts the "under N words" constraint from the
// system instruction, returning 150 if absent.
func wordLimitFrom(systemText string) int {
	if m := wordLimitRe.FindStringSubmatch(systemText); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return 150
}

func generate(maxWords int) string {
	ws := make([]string, maxWords)
	for i := range ws {
		ws[i] = wordList[rand.IntN(len(wordList))]
	}
	return strings.Join(ws, " ") + "."
}
