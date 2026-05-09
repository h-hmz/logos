package agent

import (
	"context"
	"sync"
)

var llmCache sync.Map // map[string]llm

// getLLM returns a cached llm for the given model ID, constructing one on
// first call against the OpenAI-compatible client.
func getLLM(_ context.Context, modelID string) (llm, error) {
	if v, ok := llmCache.Load(modelID); ok {
		return v.(llm), nil
	}
	m, err := newOpenAICompatLLM(modelID)
	if err != nil {
		return nil, err
	}
	actual, _ := llmCache.LoadOrStore(modelID, llm(m))
	return actual.(llm), nil
}
