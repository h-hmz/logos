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

// ElevenLabs TTS: streams raw 24 kHz 16-bit mono LE PCM via chunked HTTP.
// Flash v2.5 model has ~400 ms time-to-first-audio.

const (
	elevenLabsStreamEndpoint = "https://api.elevenlabs.io/v1/text-to-speech/%s/stream?output_format=pcm_24000"
	elevenLabsSampleRate     = 24000
	defaultElevenLabsModel   = "eleven_flash_v2_5"
	// DefaultElevenLabsVoice is Adam (male) — a neutral fallback when the
	// speaker's gender can't be determined.
	DefaultElevenLabsVoice = "pNInz6obpgDQGcFmaJgB"
)

func init() {
	Register("elevenlabs", newElevenLabsClient)
}

type elevenLabsClient struct {
	apiKey string
	model  string
	http   *http.Client
}

func newElevenLabsClient(_ context.Context) (Synthesizer, error) {
	key := config.ElevenLabsAPIKey()
	if key == "" {
		return nil, fmt.Errorf("elevenlabs tts: ELEVENLABS_API_KEY not set")
	}
	model := config.TTSModel()
	if !strings.HasPrefix(model, "eleven_") {
		model = defaultElevenLabsModel
	}
	return &elevenLabsClient{apiKey: key, model: model, http: &http.Client{Timeout: 2 * time.Minute}}, nil
}

// looksLikeElevenLabsVoiceID returns true when v appears to be a raw voice
// id (20-ish alphanumeric chars, no whitespace or punctuation). Lets users
// paste an ElevenLabs voice id directly into tts_voice if they wish.
func looksLikeElevenLabsVoiceID(v string) bool {
	if len(v) < 18 || len(v) > 40 {
		return false
	}
	for _, r := range v {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// resolveElevenLabsVoice passes through raw voice ids; anything else (empty
// or a non-id string) falls back to Adam. Per-philosopher voices come from
// tts_voices.elevenlabs in the YAMLs.
func resolveElevenLabsVoice(v string) string {
	if looksLikeElevenLabsVoiceID(v) {
		return v
	}
	return DefaultElevenLabsVoice
}

func (c *elevenLabsClient) buildRequest(ctx context.Context, text, voice string) (*http.Request, error) {
	voiceID := resolveElevenLabsVoice(voice)
	url := fmt.Sprintf(elevenLabsStreamEndpoint, voiceID)
	body := map[string]any{
		"text":     text,
		"model_id": c.model,
		"voice_settings": map[string]any{
			"stability":        0.5,
			"similarity_boost": 0.75,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("xi-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/pcm")
	return req, nil
}

func (c *elevenLabsClient) Synthesize(ctx context.Context, text, voice, style string) ([]byte, error) {
	// ElevenLabs has no dedicated "style instructions" param; prepending the
	// style to the text would be read literally, so we drop it here. Per-
	// philosopher flavor should come from voice selection + voice_settings.
	_ = style
	req, err := c.buildRequest(ctx, text, voice)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs tts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("elevenlabs tts: status %d: %s", resp.StatusCode, string(b))
	}
	pcm, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return wrapWAV(pcm, elevenLabsSampleRate, 1, 16), nil
}

func (c *elevenLabsClient) SynthesizeStream(
	ctx context.Context,
	text, voice, style string,
	onStart func(sampleRate int),
	w io.Writer,
) error {
	_ = style
	req, err := c.buildRequest(ctx, text, voice)
	if err != nil {
		return err
	}
	startT := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("elevenlabs tts stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("elevenlabs tts: status %d: %s", resp.StatusCode, string(b))
	}
	onStart(elevenLabsSampleRate)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 8192)
	firstLogged := false
	chunkCount := 0
	totalBytes := 0
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if !firstLogged {
				slog.InfoContext(ctx, "tts[elevenlabs]: first chunk",
					"latency", time.Since(startT), "model", c.model)
				firstLogged = true
			}
			chunkCount++
			totalBytes += n
			if _, werr := w.Write(buf[:n]); werr != nil {
				return fmt.Errorf("elevenlabs tts stream: write: %w", werr)
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("elevenlabs tts stream: read: %w", rerr)
		}
	}
	slog.InfoContext(ctx, "tts[elevenlabs]: stream done",
		"chunks", chunkCount,
		"bytes", totalBytes,
		"total_latency", time.Since(startT))
	return nil
}
