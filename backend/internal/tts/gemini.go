package tts

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/h-hmz/logos/internal/config"
)

// DefaultGeminiVoice is used when no per-speaker voice is configured.
const DefaultGeminiVoice = "Kore"

// buildGeminiPrompt combines a style directive with the text to speak using
// the colon-delimited pattern from Google's TTS docs (e.g.
// `Say cheerfully: Have a wonderful day!`). Gemini TTS treats everything
// before the colon as a directive and speaks only what follows — keeping
// them on a single logical block avoids the model reading the directive
// aloud, which the previous `\n\n` separator was prone to.
func buildGeminiPrompt(text, style string) string {
	style = strings.TrimSpace(style)
	if style == "" {
		return text
	}
	style = strings.TrimRight(style, ".!?:;, ")
	return style + ": " + text
}

// geminiAPIOnlyModels lists TTS models that are only served on the public
// Gemini API (not yet available on Vertex AI). Selecting one of these
// requires TTS_API_KEY / GEMINI_API_KEY / GOOGLE_API_KEY to be set.
var geminiAPIOnlyModels = map[string]bool{
	"gemini-2.5-pro-preview-tts": true,
}

func init() {
	Register("gemini", newGeminiClient)
}

// isGeminiAPIOnly reports whether model is only reachable via the public
// Gemini API backend.
func isGeminiAPIOnly(model string) bool { return geminiAPIOnlyModels[model] }

type geminiClient struct {
	inner *genai.Client
	model string
}

func newGeminiClient(ctx context.Context) (Synthesizer, error) {
	model := config.TTSModel()
	apiKey := config.TTSAPIKey()
	apiOnly := isGeminiAPIOnly(model)

	// API-only models (e.g. Pro preview TTS) cannot run on Vertex — require
	// an explicit API key and fail fast with a clear message otherwise.
	if apiOnly && apiKey == "" {
		return nil, fmt.Errorf(
			"tts model %q is only available on the Gemini API backend; "+
				"set TTS_API_KEY (or GOOGLE_API_KEY) in the environment",
			model,
		)
	}

	// Pick the backend:
	//   - api-only model → force Gemini API
	//   - api key set → prefer Gemini API (lets the rest of the app run on
	//     Vertex while TTS runs on the public API)
	//   - else inherit the app backend (Vertex or Gemini API)
	cfg := &genai.ClientConfig{}
	switch {
	case apiOnly:
		cfg.Backend = genai.BackendGeminiAPI
		cfg.APIKey = apiKey
	case apiKey != "":
		cfg.Backend = genai.BackendGeminiAPI
		cfg.APIKey = apiKey
	case config.UseVertexAI():
		cfg.Backend = genai.BackendVertexAI
		cfg.Project = config.Project()
		cfg.Location = config.Location()
	default:
		cfg.Backend = genai.BackendGeminiAPI
	}

	c, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new genai client: %w", err)
	}
	return &geminiClient{inner: c, model: model}, nil
}

func (c *geminiClient) Synthesize(ctx context.Context, text, voice, style string) ([]byte, error) {
	if voice == "" {
		voice = DefaultGeminiVoice
	}
	cfg := &genai.GenerateContentConfig{
		ResponseModalities: []string{"AUDIO"},
		SpeechConfig: &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: voice},
			},
		},
	}
	prompt := buildGeminiPrompt(text, style)
	resp, err := c.inner.Models.GenerateContent(
		ctx,
		c.model,
		[]*genai.Content{{Role: "user", Parts: []*genai.Part{genai.NewPartFromText(prompt)}}},
		cfg,
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

// SynthesizeStream streams raw PCM chunks as they arrive from the model.
// Implements tts.StreamSynthesizer.
func (c *geminiClient) SynthesizeStream(
	ctx context.Context,
	text, voice, style string,
	onStart func(sampleRate int),
	w io.Writer,
) error {
	if voice == "" {
		voice = DefaultGeminiVoice
	}
	prompt := buildGeminiPrompt(text, style)
	contents := []*genai.Content{{Role: "user", Parts: []*genai.Part{genai.NewPartFromText(prompt)}}}
	cfg := &genai.GenerateContentConfig{
		ResponseModalities: []string{"AUDIO"},
		SpeechConfig: &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: voice},
			},
		},
	}

	flusher, _ := w.(http.Flusher)
	started := false
	startT := time.Now()
	chunkCount := 0
	totalBytes := 0
	for resp, err := range c.inner.Models.GenerateContentStream(ctx, c.model, contents, cfg) {
		if err != nil {
			return fmt.Errorf("gemini tts stream: %w", err)
		}
		pcm, mime := extractGeminiChunk(resp)
		if len(pcm) == 0 {
			continue
		}
		chunkCount++
		totalBytes += len(pcm)
		slog.InfoContext(ctx, "tts[gemini]: chunk",
			"chunk", chunkCount,
			"size", len(pcm),
			"elapsed", time.Since(startT),
			"model", c.model)
		if !started {
			onStart(sampleRateFromMime(mime))
			started = true
		}
		if _, err := w.Write(pcm); err != nil {
			return fmt.Errorf("gemini tts stream: write: %w", err)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	slog.InfoContext(ctx, "tts[gemini]: stream done",
		"chunks", chunkCount,
		"bytes", totalBytes,
		"total_latency", time.Since(startT))
	if !started {
		return fmt.Errorf("gemini tts stream: no audio produced")
	}
	return nil
}

// extractGeminiChunk returns the first audio part in resp, or (nil, "") if
// the response carries no audio. Unlike extractGeminiAudio it does not
// error — empty chunks are expected during streaming.
func extractGeminiChunk(resp *genai.GenerateContentResponse) ([]byte, string) {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil, ""
	}
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.InlineData != nil && len(part.InlineData.Data) > 0 {
			return part.InlineData.Data, part.InlineData.MIMEType
		}
	}
	return nil, ""
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
