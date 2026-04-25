// Package tts synthesizes speech through Google's Gemini TTS models.
// A single process-wide client is constructed lazily on first use.
package tts

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"sync"
)

var (
	sharedMu sync.Mutex
	shared   *geminiClient
)

// client returns the process-wide Gemini TTS client, constructing it on
// first use. The factory runs exactly once; subsequent calls return the
// cached client.
func client(ctx context.Context) (*geminiClient, error) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if shared != nil {
		return shared, nil
	}
	c, err := newGeminiClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("tts: init gemini client: %w", err)
	}
	shared = c
	return shared, nil
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
