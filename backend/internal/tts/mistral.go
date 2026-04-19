package tts

import (
	"context"
	"fmt"
)

func init() {
	Register("mistral", newMistralClient)
}

type mistralClient struct {
	apiKey string
	model  string
}

func newMistralClient(_ context.Context) (Synthesizer, error) {
	key := ""
	// Pull via config accessor when implemented; left blank intentionally so
	// the stub surfaces a clear error until wired up.
	return &mistralClient{apiKey: key, model: ""}, nil
}

// Synthesize is a placeholder for Voxtral TTS. Wire up the Mistral HTTP
// endpoint here when switching: it returns audio bytes which can be wrapped
// via wrapWAV if the API yields raw PCM, or passed through if it returns WAV.
func (c *mistralClient) Synthesize(_ context.Context, _, _, _ string) ([]byte, error) {
	return nil, fmt.Errorf("mistral tts: not implemented")
}
