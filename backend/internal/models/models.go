// Package models defines the hardcoded allowlist of LLM models a caller may
// choose as a Debater Model (see CONTEXT.md). It governs only caller-supplied
// per-debater models; the operator-set default model (config.MODEL) is trusted
// and not gated by this list.
package models

import "sort"

// allowed is the set of model IDs permitted as Debater Models. IDs are
// provider/model slugs passed verbatim to the OpenAI-compatible endpoint
// (OpenRouter by default), so they must match the provider's spelling exactly.
var allowed = map[string]struct{}{
	"deepseek/deepseek-v4-pro":  {},
	"qwen/qwen3-235b-a22b-2507": {},
	"z-ai/glm-4.6":              {},
}

// Allowed reports whether id is an authorized Debater Model.
func Allowed(id string) bool {
	_, ok := allowed[id]
	return ok
}

// List returns the authorized model IDs in sorted order, for the GET /models
// endpoint that backs the (future) model-selection dropdown.
func List() []string {
	out := make([]string, 0, len(allowed))
	for id := range allowed {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
