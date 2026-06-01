package quota

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
)

// approxEqual compares USD sums tolerant of float accumulation error.
func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMemoryStore_GetMissingIsNotFound(t *testing.T) {
	s := NewMemoryStore()
	if _, err := s.Get(context.Background(), "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_AddAccumulates(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	if err := s.Add(ctx, "u1", 0.10, 1.0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(ctx, "u1", 0.05, 1.0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	acct, err := s.Get(ctx, "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !approxEqual(acct.SpentUSD, 0.15) {
		t.Fatalf("SpentUSD = %v, want 0.15", acct.SpentUSD)
	}
	if acct.Version != 2 {
		t.Fatalf("Version = %d, want 2", acct.Version)
	}
}

func TestMemoryStore_AddSnapshotsLimitOnCreateOnly(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	// First write seeds the account's limit; a later write with a different
	// seed must not change the already-snapshotted limit.
	_ = s.Add(ctx, "u1", 0.10, 1.0)
	_ = s.Add(ctx, "u1", 0.10, 5.0)
	acct, _ := s.Get(ctx, "u1")
	if acct.SpendLimitUSD != 1.0 {
		t.Fatalf("SpendLimitUSD = %v, want 1.0 (limit must be fixed at creation)", acct.SpendLimitUSD)
	}
}

func TestMemoryStore_GetReturnsCopy(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.Add(ctx, "u1", 1.0, 1.0)
	acct, _ := s.Get(ctx, "u1")
	acct.SpentUSD = 999 // mutating the returned copy must not affect the store
	again, _ := s.Get(ctx, "u1")
	if again.SpentUSD != 1.0 {
		t.Fatalf("store mutated through returned pointer: SpentUSD = %v", again.SpentUSD)
	}
}

func TestMemoryStore_AddConcurrent(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_ = s.Add(ctx, "u1", 0.01, 1.0)
		}()
	}
	wg.Wait()
	acct, _ := s.Get(ctx, "u1")
	if acct.Version != n {
		t.Fatalf("Version = %d, want %d (lost update under concurrency)", acct.Version, n)
	}
}

func TestInFlight_TryAcquireBlocksSecond(t *testing.T) {
	f := NewInFlight()
	if !f.TryAcquire("u1") {
		t.Fatal("first TryAcquire should succeed")
	}
	if f.TryAcquire("u1") {
		t.Fatal("second TryAcquire for same uid should fail while held")
	}
	// A different user is unaffected.
	if !f.TryAcquire("u2") {
		t.Fatal("TryAcquire for a different uid should succeed")
	}
	f.Release("u1")
	if !f.TryAcquire("u1") {
		t.Fatal("TryAcquire should succeed after Release")
	}
}

func TestInFlight_ReleaseUnheldIsSafe(t *testing.T) {
	f := NewInFlight()
	f.Release("never-held") // must not panic
}
