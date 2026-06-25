package tts

import (
	"context"
	"errors"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
)

// GCSNarrationStore persists narrations to a Cloud Storage bucket in a Cloud Run
// environment. Only the bucket handle is stored; it retains the underlying
// client, so there is nothing to close separately.
type GCSNarrationStore struct {
	bucket *storage.BucketHandle
}

// NewGCSNarrationStore constructs a store bound to the named bucket.
func NewGCSNarrationStore(ctx context.Context, bucket string) (*GCSNarrationStore, error) {
	if bucket == "" {
		return nil, errors.New("tts: bucket is required")
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("tts: storage client: %w", err)
	}
	return &GCSNarrationStore{bucket: client.Bucket(bucket)}, nil
}

func (s *GCSNarrationStore) Open(ctx context.Context, key string) (io.ReadCloser, bool, error) {
	r, err := s.bucket.Object(key).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("tts: open %q: %w", key, err)
	}
	return r, true, nil
}

// Put streams data into a new object, finalized only on Close. On a write
// failure the context is cancelled so the resumable upload is abandoned rather
// than finalized — a key therefore holds the complete object or nothing.
func (s *GCSNarrationStore) Put(ctx context.Context, key string, data []byte) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := s.bucket.Object(key).NewWriter(ctx)
	w.ContentType = fmt.Sprintf("audio/L16;rate=%d;channels=1", SampleRate)
	if _, err := w.Write(data); err != nil {
		cancel()
		return fmt.Errorf("tts: put write %q: %w", key, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("tts: put close %q: %w", key, err)
	}
	return nil
}

// Exists reports whether an object is stored at key, fetching only its metadata
// (Attrs). Cheaper than Open for a pure presence check.
func (s *GCSNarrationStore) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.bucket.Object(key).Attrs(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("tts: exists %q: %w", key, err)
	}
	return true, nil
}
