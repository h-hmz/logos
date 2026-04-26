// Package gibberish provides a local model.LLM that generates random
// philosophical-sounding nonsense — no network calls required.
// Used as the default when no MODEL env var is set.
package gibberish

import (
	"context"
	"iter"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

var wordLimitRe = regexp.MustCompile(`under (\d+) words`)

const ModelID = "local-gibberish"

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

// Model satisfies model.LLM with locally generated gibberish text.
type Model struct{}

func (m *Model) Name() string { return ModelID }

func (m *Model) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	text := generate(wordLimitFrom(req))
	inputTokens := int32(requestChars(req) / 4)
	outputTokens := int32(len(text) / 4)
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content: &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{genai.NewPartFromText(text)},
			},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     inputTokens,
				CandidatesTokenCount: outputTokens,
				TotalTokenCount:      inputTokens + outputTokens,
			},
			TurnComplete: true,
		}, nil)
	}
}

// requestChars sums the character lengths of all text in the request.
func requestChars(req *model.LLMRequest) int {
	if req == nil {
		return 0
	}
	n := 0
	if req.Config != nil && req.Config.SystemInstruction != nil {
		for _, p := range req.Config.SystemInstruction.Parts {
			n += len(p.Text)
		}
	}
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			n += len(p.Text)
		}
	}
	return n
}

// wordLimitFrom extracts the "under N words" constraint from the system
// instruction the ADK injects into every LLMRequest, returning 150 if absent.
func wordLimitFrom(req *model.LLMRequest) int {
	if req == nil || req.Config == nil || req.Config.SystemInstruction == nil {
		return 150
	}
	for _, p := range req.Config.SystemInstruction.Parts {
		if m := wordLimitRe.FindStringSubmatch(p.Text); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
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
