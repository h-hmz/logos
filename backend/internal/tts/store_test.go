package tts

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestNarrationKey(t *testing.T) {
	base := NarrationKey("d1", "hello world", "Kore", "cheerful", "gemini")

	// Stable: same inputs → same key.
	if got := NarrationKey("d1", "hello world", "Kore", "cheerful", "gemini"); got != base {
		t.Fatalf("key not stable: %q != %q", got, base)
	}

	// Shape: "{debateID}/{64-hex}.pcm".
	prefix := "d1/"
	if !strings.HasPrefix(base, prefix) || !strings.HasSuffix(base, ".pcm") {
		t.Fatalf("unexpected key shape: %q", base)
	}
	if hexLen := len(strings.TrimSuffix(strings.TrimPrefix(base, prefix), ".pcm")); hexLen != 64 {
		t.Fatalf("expected 64-char sha256 hex, got %d in %q", hexLen, base)
	}

	// Each input participates in the hash.
	for name, k := range map[string]string{
		"text":  NarrationKey("d1", "HELLO WORLD", "Kore", "cheerful", "gemini"),
		"voice": NarrationKey("d1", "hello world", "Puck", "cheerful", "gemini"),
		"style": NarrationKey("d1", "hello world", "Kore", "sombre", "gemini"),
		"model": NarrationKey("d1", "hello world", "Kore", "cheerful", "other"),
	} {
		if k == base {
			t.Errorf("changing %s did not change the key", name)
		}
	}

	// NUL-delimiting prevents field-boundary collisions.
	if NarrationKey("d1", "ab", "c", "", "") == NarrationKey("d1", "a", "bc", "", "") {
		t.Error("field boundary collision: (ab,c) keyed same as (a,bc)")
	}

	// The debate ID is only a path prefix, not part of the content hash.
	a := strings.TrimPrefix(NarrationKey("d1", "x", "v", "s", "m"), "d1/")
	b := strings.TrimPrefix(NarrationKey("d2", "x", "v", "s", "m"), "d2/")
	if a != b {
		t.Errorf("debate ID leaked into content hash: %q vs %q", a, b)
	}
}

func TestMemoryNarrationStore(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryNarrationStore()

	// Miss on an absent key.
	if _, ok, err := s.Open(ctx, "d1/missing.pcm"); err != nil || ok {
		t.Fatalf("absent key: ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	// Round-trip.
	want := []byte{0, 1, 2, 3, 255}
	if err := s.Put(ctx, "d1/k.pcm", want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, ok, err := s.Open(ctx, "d1/k.pcm")
	if err != nil || !ok {
		t.Fatalf("Open after Put: ok=%v err=%v", ok, err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != string(want) {
		t.Fatalf("round-trip mismatch: %v != %v", got, want)
	}

	// Put copies its input — later mutation of the caller's slice must not
	// change the stored bytes.
	data := []byte{9, 9}
	if err := s.Put(ctx, "d1/copy.pcm", data); err != nil {
		t.Fatalf("Put: %v", err)
	}
	data[0] = 0
	rc, _, _ = s.Open(ctx, "d1/copy.pcm")
	got, _ = io.ReadAll(rc)
	rc.Close()
	if got[0] != 9 {
		t.Fatalf("Put did not copy: stored byte mutated to %d", got[0])
	}
}
