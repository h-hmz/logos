package agent

import (
	"context"
	"sync"
)

var llmCache sync.Map // map[string]LLM

// Get returns a cached LLM for the given model ID, constructing one on
// first call against the OpenAI-compatible client.
func Get(_ context.Context, modelID string) (LLM, error) {
	if v, ok := llmCache.Load(modelID); ok {
		return v.(LLM), nil
	}
	m, err := newOpenAICompatLLM(modelID)
	if err != nil {
		return nil, err
	}
	actual, _ := llmCache.LoadOrStore(modelID, LLM(m))
	return actual.(LLM), nil
}
