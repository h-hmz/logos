// Package modelcache lazily creates and caches model.LLM objects by model ID.
// The Go ADK requires model objects rather than string IDs; this package bridges
// that gap so agent factories can keep accepting a simple string.
package modelcache

import (
	"context"
	"sync"

	"github.com/h-hmz/logos/internal/config"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/genai"
)

var cache sync.Map // map[string]model.LLM

// Get returns a cached model or creates one on first call for the given ID.
func Get(ctx context.Context, modelID string) (model.LLM, error) {
	if v, ok := cache.Load(modelID); ok {
		return v.(model.LLM), nil
	}

	cfg := &genai.ClientConfig{}
	if config.UseVertexAI() {
		cfg.Project = config.Project()
		cfg.Location = config.Location()
		cfg.Backend = genai.BackendVertexAI
	} else {
		cfg.Backend = genai.BackendGeminiAPI
	}

	m, err := gemini.NewModel(ctx, modelID, cfg)
	if err != nil {
		return nil, err
	}

	actual, _ := cache.LoadOrStore(modelID, m)
	return actual.(model.LLM), nil
}
