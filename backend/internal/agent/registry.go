package agent

import (
	"context"
	"sync"
)

var (
	llmCache     sync.Map // map[string]LLM
	llmFactories sync.Map // map[string]func() LLM
)

// RegisterFactory wires a non-network LLM (typically a mock used in
// local dev or tests) to a model ID. Called from package-level init()
// in the implementing package; the side-effect blank import in main.go
// triggers registration at startup.
func RegisterFactory(modelID string, factory func() LLM) {
	llmFactories.Store(modelID, factory)
}

// Get returns a cached LLM for the given model ID, constructing one on
// first call. Registered factories take precedence over the OpenAI-
// compatible client — that's what routes "local-gibberish" to the
// in-process mock when gibberish is blank-imported.
func Get(_ context.Context, modelID string) (LLM, error) {
	if v, ok := llmCache.Load(modelID); ok {
		return v.(LLM), nil
	}

	var llm LLM
	if v, ok := llmFactories.Load(modelID); ok {
		factory := v.(func() LLM)
		llm = factory()
	} else {
		m, err := newOpenAICompatLLM(modelID)
		if err != nil {
			return nil, err
		}
		llm = m
	}
	actual, _ := llmCache.LoadOrStore(modelID, llm)
	return actual.(LLM), nil
}
