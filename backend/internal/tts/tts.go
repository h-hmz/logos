// Package tts synthesizes speech through any OpenAI-compatible
// /audio/speech endpoint (configured via LLM_BASE_URL — defaults to
// OpenRouter, which routes to Gemini TTS). See ADR 0002, 0007.
// A single process-wide client is constructed lazily on first use.
package tts

import (
	"context"
	"fmt"
	"sync"
)

// geminiTTSSampleRate is the fixed output sample rate of the Gemini TTS
// family (24 kHz, 16-bit, mono). OpenRouter's /audio/speech endpoint
// does not return a MIME header, so we hardcode it (per ADR 0002 rev).
const geminiTTSSampleRate = 24000

// SampleRate is the PCM sample rate (Hz) of every narration: Gemini TTS emits
// 24 kHz, 16-bit, mono. Stored narrations are raw PCM at this rate, so a caller
// serving a cached narration sets Content-Type audio/L16;rate=SampleRate.
const SampleRate = geminiTTSSampleRate

var (
	clientOnce sync.Once
	shared     *openAICompatTTSClient
	sharedErr  error
)

// client returns the process-wide TTS client.
func client(_ context.Context) (*openAICompatTTSClient, error) {
	clientOnce.Do(func() {
		c, err := newOpenAICompatTTSClient(context.Background())
		if err != nil {
			sharedErr = fmt.Errorf("tts: init client: %w", err)
			return
		}
		shared = c
	})
	return shared, sharedErr
}
