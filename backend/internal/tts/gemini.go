package tts

import (
	"context"
	"fmt"

	"google.golang.org/genai"

	"github.com/h-hmz/logos/internal/config"
)

// DefaultGeminiVoice is used when no per-speaker voice is configured.
const DefaultGeminiVoice = "Kore"

func init() {
	Register("gemini", newGeminiClient)
}

type geminiClient struct {
	inner *genai.Client
	model string
}

func newGeminiClient(ctx context.Context) (Synthesizer, error) {
	// If a TTS/Gemini API key is set, force the Gemini API backend for TTS —
	// useful when the rest of the app runs on Vertex but you want TTS on the
	// public API. Otherwise inherit the app's backend (Vertex or Gemini API).
	cfg := &genai.ClientConfig{}
	if k := config.TTSAPIKey(); k != "" {
		cfg.Backend = genai.BackendGeminiAPI
		cfg.APIKey = k
	} else if config.UseVertexAI() {
		cfg.Project = config.Project()
		cfg.Location = config.Location()
		cfg.Backend = genai.BackendVertexAI
	} else {
		cfg.Backend = genai.BackendGeminiAPI
	}

	c, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new genai client: %w", err)
	}
	return &geminiClient{inner: c, model: config.TTSModel()}, nil
}

func (c *geminiClient) Synthesize(ctx context.Context, text, voice, style string) ([]byte, error) {
	if voice == "" {
		voice = DefaultGeminiVoice
	}
	prompt := text
	if style != "" {
		prompt = style + "\n\n" + text
	}

	resp, err := c.inner.Models.GenerateContent(
		ctx,
		c.model,
		[]*genai.Content{{Role: "user", Parts: []*genai.Part{genai.NewPartFromText(prompt)}}},
		&genai.GenerateContentConfig{
			ResponseModalities: []string{"AUDIO"},
			SpeechConfig: &genai.SpeechConfig{
				VoiceConfig: &genai.VoiceConfig{
					PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: voice},
				},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("gemini synthesize: %w", err)
	}

	pcm, mime, err := extractGeminiAudio(resp)
	if err != nil {
		return nil, err
	}
	sampleRate := sampleRateFromMime(mime)
	return wrapWAV(pcm, sampleRate, 1, 16), nil
}

func extractGeminiAudio(resp *genai.GenerateContentResponse) ([]byte, string, error) {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil, "", fmt.Errorf("gemini tts: empty response")
	}
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.InlineData != nil && len(part.InlineData.Data) > 0 {
			return part.InlineData.Data, part.InlineData.MIMEType, nil
		}
	}
	return nil, "", fmt.Errorf("gemini tts: response contained no audio data")
}
