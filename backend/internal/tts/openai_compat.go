package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/h-hmz/logos/internal/config"
)

// buildGeminiPrompt combines a style directive with the text using
// the colon-delimited pattern from Google's TTS docs (e.g.
// `Say cheerfully: Have a wonderful day!`). Gemini TTS treats
// everything before the colon as a directive and speaks only what
// follows; we keep them on a single logical block so the model
// doesn't read the directive aloud.
func buildGeminiPrompt(text, style string) string {
	style = strings.TrimSpace(style)
	if style == "" {
		return text
	}
	style = strings.TrimRight(style, ".!?:;, ")
	return style + ": " + text
}

// openAICompatTTSClient calls any OpenAI-compatible /audio/speech
// endpoint. Configured by default for OpenRouter (see ADR 0007),
// overridable via LLM_BASE_URL.
type openAICompatTTSClient struct {
	client openai.Client
	model  string
}

func newOpenAICompatTTSClient(_ context.Context) (*openAICompatTTSClient, error) {
	apiKey := config.LLMAPIKey()
	if apiKey == "" {
		return nil, errors.New("LLM_API_KEY not set")
	}
	c := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(config.LLMBaseURL()),
		option.WithHeader("HTTP-Referer", "https://github.com/h-hmz/logos"),
		option.WithHeader("X-Title", "Logos"),
	)
	return &openAICompatTTSClient{client: c, model: config.TTSModel()}, nil
}

// SynthesizeStream streams raw 16-bit little-endian mono PCM as bytes
// arrive from the model. onStart fires exactly once with the sample
// rate the moment we begin writing — use it to flush response headers
// before writing PCM bytes to w.
func SynthesizeStream(
	ctx context.Context,
	text, voice, style string,
	onStart func(sampleRate int),
	w io.Writer,
) error {
	c, err := client(ctx)
	if err != nil {
		return err
	}
	return c.synthesizeStream(ctx, text, voice, style, onStart, w)
}

func (c *openAICompatTTSClient) synthesizeStream(
	ctx context.Context,
	text, voice, style string,
	onStart func(sampleRate int),
	w io.Writer,
) error {
	flusher, _ := w.(http.Flusher)
	startT := time.Now()

	// One /audio/speech call for the whole text. Gemini TTS caps output at
	// 3840 audio tokens (~2 minutes) per call and truncates silently beyond
	// it (ADR 0002 rev 2026-07-06); we accept the truncation rather than
	// splitting into segments, which skipped chunks at segment boundaries.
	prompt := buildGeminiPrompt(text, style)
	params := openai.AudioSpeechNewParams{
		Model:          c.model,
		Input:          prompt,
		Voice:          openai.AudioSpeechNewParamsVoice(voice),
		ResponseFormat: openai.AudioSpeechNewParamsResponseFormatPCM,
	}
	resp, err := c.client.Audio.Speech.New(ctx, params)
	if err != nil {
		return fmt.Errorf("tts: %w", err)
	}
	defer resp.Body.Close()
	onStart(geminiTTSSampleRate)

	totalBytes := 0
	buf := make([]byte, 8192)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return fmt.Errorf("tts: write: %w", werr)
			}
			totalBytes += n
			if flusher != nil {
				flusher.Flush()
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return fmt.Errorf("tts: read: %w", rerr)
		}
	}
	slog.InfoContext(ctx, "tts: stream done",
		"bytes", totalBytes,
		"total_latency", time.Since(startT))
	return nil
}
