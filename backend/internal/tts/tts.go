// Package tts synthesizes speech through a pluggable provider interface.
// Providers (Gemini, Mistral/Voxtral, ElevenLabs, ...) register themselves
// via init() and are selected at runtime by config.TTSProvider().
package tts

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"sync"

	"github.com/h-hmz/logos/internal/config"
)

// Synthesizer produces a self-contained WAV byte slice for the given text.
// voice and style are provider-specific; empty strings request provider
// defaults. Implementations must be safe for concurrent use.
type Synthesizer interface {
	Synthesize(ctx context.Context, text, voice, style string) ([]byte, error)
}

// Factory builds a Synthesizer on demand. Factories run once per process
// (results are cached by Shared).
type Factory func(ctx context.Context) (Synthesizer, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}

	sharedMu     sync.Mutex
	sharedClient Synthesizer
)

// Register associates a provider name (e.g. "gemini", "mistral") with its
// factory. Called from provider-specific init() functions.
func Register(name string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = f
}

// Shared returns the process-wide TTS client for the configured provider,
// creating it on first use. Returns (nil, nil) when TTS is disabled.
func Shared(ctx context.Context) (Synthesizer, error) {
	if !config.TTSEnabled() {
		return nil, nil
	}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedClient != nil {
		return sharedClient, nil
	}

	name := config.TTSProvider()
	registryMu.RLock()
	factory, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tts: unknown provider %q", name)
	}
	c, err := factory(ctx)
	if err != nil {
		return nil, fmt.Errorf("tts: init provider %q: %w", name, err)
	}
	sharedClient = c
	return sharedClient, nil
}

var rateRe = regexp.MustCompile(`rate=(\d+)`)

// sampleRateFromMime parses "audio/L16;rate=24000" style mime strings.
// Defaults to 24000 when the rate parameter is absent.
func sampleRateFromMime(mime string) int {
	if m := rateRe.FindStringSubmatch(mime); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}
	return 24000
}

// wrapWAV builds a RIFF/WAVE header around signed little-endian PCM samples.
func wrapWAV(pcm []byte, sampleRate, channels, bitsPerSample int) []byte {
	var buf bytes.Buffer
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataLen := len(pcm)

	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(channels))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataLen))
	buf.Write(pcm)
	return buf.Bytes()
}
