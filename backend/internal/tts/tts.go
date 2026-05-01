// Package tts synthesizes speech through any OpenAI-compatible
// /audio/speech endpoint (configured via LLM_BASE_URL — defaults to
// OpenRouter, which routes to Gemini TTS). See ADR 0002, 0007.
// A single process-wide client is constructed lazily on first use.
package tts

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sync"
)

// geminiTTSSampleRate is the fixed output sample rate of the Gemini TTS
// family (24 kHz, 16-bit, mono). OpenRouter's /audio/speech endpoint
// does not return a MIME header, so we hardcode it (per ADR 0002 rev).
const geminiTTSSampleRate = 24000

var (
	sharedMu sync.Mutex
	shared   *openAICompatTTSClient
)

// client returns the process-wide TTS client, constructing it on first
// use. The factory runs exactly once; subsequent calls return the
// cached client.
func client(ctx context.Context) (*openAICompatTTSClient, error) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if shared != nil {
		return shared, nil
	}
	c, err := newOpenAICompatTTSClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("tts: init client: %w", err)
	}
	shared = c
	return shared, nil
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
