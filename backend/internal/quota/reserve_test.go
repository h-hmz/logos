package quota

import (
	"context"
	"sync"
	"testing"
)

func TestMemoryStore_Reserve(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	// Affordable reserve on a fresh account seeds the limit and charges spend.
	if ok, err := s.Reserve(ctx, "u", 0.25, 1.0); err != nil || !ok {
		t.Fatalf("first reserve: ok=%v err=%v", ok, err)
	}
	// Reservations accumulate while they fit (0.25 + 0.25 + 0.25 + 0.25 = 1.0).
	for i := range 3 {
		if ok, _ := s.Reserve(ctx, "u", 0.25, 1.0); !ok {
			t.Fatalf("reserve %d should fit", i+2)
		}
	}
	// The next would exceed the budget → refused with no write.
	if ok, _ := s.Reserve(ctx, "u", 0.25, 1.0); ok {
		t.Fatal("over-budget reserve should be refused")
	}
	acct, _ := s.Get(ctx, "u")
	if acct.SpentUSD != 1.0 {
		t.Fatalf("spent after refused reserve = %v, want 1.0", acct.SpentUSD)
	}

	// Settlement refunds via a negative Add: reserved 0.25 but actual was 0.10.
	if err := s.Add(ctx, "u", 0.10-0.25, 1.0); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if acct, _ = s.Get(ctx, "u"); acct.SpentUSD != 0.85 {
		t.Fatalf("spent after refund = %v, want 0.85", acct.SpentUSD)
	}
}

// The whole point of reserve-then-settle (ADR 0013): concurrent reservations
// can never collectively overshoot the budget.
func TestMemoryStore_Reserve_NoConcurrentOvershoot(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	const n = 64
	won := make(chan bool, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _ := s.Reserve(ctx, "u", 0.25, 1.0) // 0.25 is exact in float64
			won <- ok
		}()
	}
	wg.Wait()
	close(won)
	count := 0
	for ok := range won {
		if ok {
			count++
		}
	}
	if count != 4 {
		t.Fatalf("winners=%d, want exactly 4 (4 × 0.25 = 1.0 budget)", count)
	}
	if acct, _ := s.Get(ctx, "u"); acct.SpentUSD > 1.0 {
		t.Fatalf("overshoot: spent=%v > 1.0", acct.SpentUSD)
	}
}
