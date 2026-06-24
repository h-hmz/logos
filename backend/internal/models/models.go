// Package models defines the hardcoded allowlist of LLM models a caller may
// choose as a Debater Model (see CONTEXT.md). Every model is caller-supplied and
// must be in this list; there is no operator-set default outside it (ADR 0011).
package models

// allowed is the set of model IDs permitted as Debater Models. IDs are
// provider/model slugs passed verbatim to the OpenAI-compatible endpoint
// (OpenRouter by default), so they must match the provider's spelling exactly.
var allowed = map[string]struct{}{
	"deepseek/deepseek-v4-pro":      {},
	"qwen/qwen3-235b-a22b-2507":     {},
	"z-ai/glm-5":                    {},
	"z-ai/glm-4.5-air":              {},
	"openai/gpt-5.4-mini":           {},
	"google/gemini-3-flash-preview": {},
	"openai/gpt-5.5":                {},
}

// Allowed reports whether id is an authorized Debater Model.
func Allowed(id string) bool {
	_, ok := allowed[id]
	return ok
}
