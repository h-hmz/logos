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

// DefaultGeminiVoice is used when no per-speaker voice is configured.
const DefaultGeminiVoice = "Kore"

// maxTTSChars is the target maximum character count per /audio/speech
// call. The underlying Gemini TTS model caps output at 3840 audio
// tokens (~2 minutes of speech) per call. At ~150 wpm / ~750 chars
// per minute, 800 chars ≈ 60 s — well under the cap with room for
// slower passages. See ADR 0002.
const maxTTSChars = 800

// splitText splits text into segments no longer than maxChars,
// preferring breaks at sentence-ending punctuation (. ! ?) followed
// by whitespace.
func splitText(text string, maxChars int) []string {
	if len(text) <= maxChars {
		return []string{text}
	}
	var segments []string
	for len(text) > 0 {
		if len(text) <= maxChars {
			segments = append(segments, text)
			break
		}
		cut := maxChars
		for i := maxChars - 1; i > maxChars/2; i-- {
			if text[i] == '.' || text[i] == '!' || text[i] == '?' {
				if i+1 >= len(text) || text[i+1] == ' ' || text[i+1] == '\n' {
					cut = i + 1
					break
				}
			}
		}
		if seg := strings.TrimSpace(text[:cut]); seg != "" {
			segments = append(segments, seg)
		}
		text = strings.TrimSpace(text[cut:])
	}
	return segments
}

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

// Synthesize returns a self-contained WAV for the given text. voice
// and style are optional: empty voice falls back to DefaultGeminiVoice;
// empty style omits the leading directive in the prompt.
func Synthesize(ctx context.Context, text, voice, style string) ([]byte, error) {
	c, err := client(ctx)
	if err != nil {
		return nil, err
	}
	return c.synthesize(ctx, text, voice, style)
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

func (c *openAICompatTTSClient) synthesize(ctx context.Context, text, voice, style string) ([]byte, error) {
	var pcm []byte
	err := c.synthesizeStream(ctx, text, voice, style,
		func(int) {},
		writerFunc(func(p []byte) (int, error) {
			pcm = append(pcm, p...)
			return len(p), nil
		}),
	)
	if err != nil {
		return nil, err
	}
	return wrapWAV(pcm, geminiTTSSampleRate, 1, 16), nil
}

func (c *openAICompatTTSClient) synthesizeStream(
	ctx context.Context,
	text, voice, style string,
	onStart func(sampleRate int),
	w io.Writer,
) error {
	if voice == "" {
		voice = DefaultGeminiVoice
	}
	flusher, _ := w.(http.Flusher)
	started := false
	startT := time.Now()
	totalBytes := 0

	segments := splitText(text, maxTTSChars)
	for segIdx, seg := range segments {
		prompt := buildGeminiPrompt(seg, style)
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
		if !started {
			onStart(geminiTTSSampleRate)
			started = true
		}
		segBytes := 0
		buf := make([]byte, 8192)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					resp.Body.Close()
					return fmt.Errorf("tts: write: %w", werr)
				}
				segBytes += n
				totalBytes += n
				if flusher != nil {
					flusher.Flush()
				}
			}
			if errors.Is(rerr, io.EOF) {
				break
			}
			if rerr != nil {
				resp.Body.Close()
				return fmt.Errorf("tts: read: %w", rerr)
			}
		}
		resp.Body.Close()
		slog.InfoContext(ctx, "tts: segment",
			"segment", segIdx+1,
			"bytes", segBytes,
			"elapsed", time.Since(startT),
			"model", c.model)
	}
	slog.InfoContext(ctx, "tts: stream done",
		"bytes", totalBytes,
		"segments", len(segments),
		"total_latency", time.Since(startT))
	if !started {
		return errors.New("tts: no audio produced")
	}
	return nil
}

// writerFunc adapts a func to io.Writer for the buffered Synthesize path.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
