package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/h-hmz/logos/internal/config"
)

// OpenAI TTS: streams 24 kHz 16-bit mono little-endian PCM over a regular
// HTTP response — genuinely progressive unlike Gemini's monolithic output.

const (
	openAIEndpoint     = "https://api.openai.com/v1/audio/speech"
	openAISampleRate   = 24000
	defaultOpenAIModel = "gpt-4o-mini-tts"
	DefaultOpenAIVoice = "alloy"
)

var openAIVoices = []string{
	"alloy", "ash", "ballad", "coral", "echo", "fable", "onyx", "nova", "sage", "shimmer",
}

func init() {
	Register("openai", newOpenAIClient)
}

type openaiClient struct {
	apiKey string
	model  string
	http   *http.Client
}

func newOpenAIClient(_ context.Context) (Synthesizer, error) {
	key := config.OpenAIAPIKey()
	if key == "" {
		return nil, fmt.Errorf("openai tts: OPENAI_API_KEY not set")
	}
	model := config.TTSModel()
	if !strings.HasPrefix(model, "gpt-") && !strings.HasPrefix(model, "tts-") {
		// Default TTS_MODEL is a Gemini id; fall back to the OpenAI default
		// so users only need to flip TTS_PROVIDER.
		model = defaultOpenAIModel
	}
	return &openaiClient{apiKey: key, model: model, http: &http.Client{Timeout: 2 * time.Minute}}, nil
}

// resolveVoice validates that v is a known OpenAI voice. Unknown inputs fall
// back to "alloy" — per-philosopher voices should be set in tts_voices.openai.
func resolveVoice(v string) string {
	if v == "" {
		return DefaultOpenAIVoice
	}
	for _, ok := range openAIVoices {
		if strings.EqualFold(v, ok) {
			return ok
		}
	}
	return DefaultOpenAIVoice
}

func (c *openaiClient) buildRequest(ctx context.Context, text, voice, style string) (*http.Request, error) {
	body := map[string]any{
		"model":           c.model,
		"input":           text,
		"voice":           resolveVoice(voice),
		"response_format": "pcm",
	}
	if style != "" {
		body["instructions"] = style
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIEndpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *openaiClient) Synthesize(ctx context.Context, text, voice, style string) ([]byte, error) {
	req, err := c.buildRequest(ctx, text, voice, style)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai tts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai tts: status %d: %s", resp.StatusCode, string(b))
	}
	pcm, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return wrapWAV(pcm, openAISampleRate, 1, 16), nil
}

func (c *openaiClient) SynthesizeStream(
	ctx context.Context,
	text, voice, style string,
	onStart func(sampleRate int),
	w io.Writer,
) error {
	req, err := c.buildRequest(ctx, text, voice, style)
	if err != nil {
		return err
	}
	startT := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("openai tts stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai tts: status %d: %s", resp.StatusCode, string(b))
	}

	onStart(openAISampleRate)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 8192)
	firstLogged := false
	totalBytes := 0
	chunkCount := 0
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if !firstLogged {
				slog.InfoContext(ctx, "tts[openai]: first chunk", "latency", time.Since(startT), "model", c.model)
				firstLogged = true
			}
			chunkCount++
			totalBytes += n
			if _, werr := w.Write(buf[:n]); werr != nil {
				return fmt.Errorf("openai tts stream: write: %w", werr)
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("openai tts stream: read: %w", rerr)
		}
	}
	slog.InfoContext(ctx, "tts[openai]: stream done",
		"chunks", chunkCount,
		"bytes", totalBytes,
		"total_latency", time.Since(startT))
	return nil
}
