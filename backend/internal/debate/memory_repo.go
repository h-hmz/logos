package debate

import (
	"context"
	"sync"
)

// MemoryRepo is a process-local Repository for tests and single-instance use.
type MemoryRepo struct {
	mu      sync.RWMutex
	debates map[string]*Persisted
}

func NewMemoryRepo() *MemoryRepo {
	return &MemoryRepo{debates: make(map[string]*Persisted)}
}

func (r *MemoryRepo) Get(_ context.Context, id string) (*Persisted, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.debates[id]
	if !ok {
		return nil, ErrNotFound
	}
	return p.Clone(), nil
}

func (r *MemoryRepo) Create(_ context.Context, p *Persisted) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.debates[p.ID]; exists {
		return ErrAlreadyExists
	}
	cp := p.Clone()
	cp.Version = 1
	r.debates[p.ID] = cp
	p.Version = 1
	return nil
}

func (r *MemoryRepo) Update(_ context.Context, p *Persisted) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.debates[p.ID]
	if !ok {
		return ErrNotFound
	}
	if existing.Version != p.Version {
		return ErrVersionConflict
	}
	cp := p.Clone()
	cp.Version = p.Version + 1
	r.debates[p.ID] = cp
	p.Version = cp.Version
	return nil
}
