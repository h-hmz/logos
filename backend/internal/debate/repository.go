package debate

import "context"

// Repository persists debates. Implementations: MemoryRepo (in-process),
// and later a Firestore-backed version for Cloud Run.
type Repository interface {
	Get(ctx context.Context, id string) (*Persisted, error)
	Create(ctx context.Context, p *Persisted) error
	// Update applies an optimistic write: fails with ErrVersionConflict if the
	// stored Version differs from p.Version. On success, p.Version is advanced
	// to the newly stored version.
	Update(ctx context.Context, p *Persisted) error
}
