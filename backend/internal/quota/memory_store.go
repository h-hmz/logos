package quota

import (
	"context"
	"sync"
)

// MemoryStore is a process-local Store for tests and single-instance dev use.
// Spend does not survive a restart.
type MemoryStore struct {
	mu       sync.RWMutex
	accounts map[string]*Account
}

// NewMemoryStore returns an empty in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{accounts: make(map[string]*Account)}
}

func (s *MemoryStore) Get(_ context.Context, uid string) (*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.accounts[uid]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (s *MemoryStore) Add(_ context.Context, uid string, costUSD, initialLimitUSD float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[uid]
	if !ok {
		a = &Account{UID: uid, SpendLimitUSD: initialLimitUSD}
		s.accounts[uid] = a
	}
	a.SpentUSD += costUSD
	a.Version++
	return nil
}
