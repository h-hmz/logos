package tts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"
)

// NarrationStore persists synthesized narrations (raw PCM) keyed by content.
// Implementations: MemoryNarrationStore (in-process, for dev/tests) and
// GCSNarrationStore (Cloud Run). The read path streams (Open → io.ReadCloser)
// because replays are the hot path; the write path takes the whole clip
// (Put([]byte)) and stores it atomically on success only. See ADR 0012.
type NarrationStore interface {
	// Open streams the stored narration at key. ok is false (with nil error)
	// when no object exists; a non-nil error is a real read failure that the
	// caller treats as a miss (fail-open).
	Open(ctx context.Context, key string) (rc io.ReadCloser, ok bool, err error)
	// Put stores data under key, overwriting. Atomic: a key holds the complete
	// object or nothing.
	Put(ctx context.Context, key string, data []byte) error
}

// NarrationKey returns the content-addressed object key for a narration. The
// bytes are a pure function of (text, voice, style, model), so those four are
// the identity; the debate ID is only a path prefix for per-debate lifecycle.
// The fields are NUL-delimited so ("ab","c") cannot collide with ("a","bc").
func NarrationKey(debateID, text, voice, style, model string) string {
	h := sha256.New()
	for _, part := range []string{text, voice, style, model} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return debateID + "/" + hex.EncodeToString(h.Sum(nil)) + ".pcm"
}

// MemoryNarrationStore is a process-local NarrationStore for tests and
// single-instance dev use. Objects do not survive a restart and are never
// evicted; use only under STORAGE_BACKEND=memory.
type MemoryNarrationStore struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

// NewMemoryNarrationStore returns an empty in-memory NarrationStore.
func NewMemoryNarrationStore() *MemoryNarrationStore {
	return &MemoryNarrationStore{objects: make(map[string][]byte)}
}

func (s *MemoryNarrationStore) Open(_ context.Context, key string) (io.ReadCloser, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.objects[key]
	if !ok {
		return nil, false, nil
	}
	// Stored slices are never mutated (Put copies), so a reader over b is safe.
	return io.NopCloser(bytes.NewReader(b)), true, nil
}

func (s *MemoryNarrationStore) Put(_ context.Context, key string, data []byte) error {
	cp := make([]byte, len(data))
	copy(cp, data)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = cp
	return nil
}
