package tts

import (
	"context"
	"fmt"
)

func init() {
	Register("elevenlabs", newElevenLabsClient)
}

type elevenLabsClient struct {
	apiKey string
	model  string
}

func newElevenLabsClient(_ context.Context) (Synthesizer, error) {
	return &elevenLabsClient{}, nil
}

// Synthesize is a placeholder for ElevenLabs. The REST endpoint returns
// MP3/PCM directly — if MP3, return bytes as-is and let the client decode;
// if PCM, wrap with wrapWAV.
func (c *elevenLabsClient) Synthesize(_ context.Context, _, _, _ string) ([]byte, error) {
	return nil, fmt.Errorf("elevenlabs tts: not implemented")
}
