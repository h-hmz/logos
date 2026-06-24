package debate

import (
	"context"
	"errors"
	"testing"

	"github.com/h-hmz/logos/internal/quota"
)

// failingStore is a quota.Store whose reads and writes always error, for
// exercising the fail-open paths.
type failingStore struct{ addCalls int }

func (f *failingStore) Get(context.Context, string) (*quota.Account, error) {
	return nil, errors.New("boom")
}
func (f *failingStore) Add(context.Context, string, float64, float64) error {
	f.addCalls++
	return errors.New("boom")
}
func (f *failingStore) Reserve(context.Context, string, float64, float64) (bool, error) {
	return false, errors.New("boom")
}

func newQuotaService(store quota.Store, defaultLimitUSD float64) *Service {
	return NewService(nil, nil, QuotaConfig{Store: store, DefaultLimitUSD: defaultLimitUSD, InFlight: quota.NewInFlight()})
}

// unlimitedQuota returns a quota config with a fresh in-memory store and a budget
// high enough that pipeline tests never hit the limit.
func unlimitedQuota() QuotaConfig {
	return QuotaConfig{Store: quota.NewMemoryStore(), DefaultLimitUSD: 1e9, InFlight: quota.NewInFlight()}
}

func TestAdmitGeneration_OverBudget(t *testing.T) {
	store := quota.NewMemoryStore()
	ctx := context.Background()
	_ = store.Add(ctx, "u1", 1.0, 1.0) // spend == limit
	s := newQuotaService(store, 1.0)

	_, err := s.admitGeneration(ctx, "u1")
	if !errors.Is(err, ErrOverBudget) {
		t.Fatalf("admitGeneration err = %v, want ErrOverBudget", err)
	}
}

func TestAdmitGeneration_UnderBudgetAdmits(t *testing.T) {
	store := quota.NewMemoryStore()
	ctx := context.Background()
	_ = store.Add(ctx, "u1", 0.5, 1.0)
	s := newQuotaService(store, 1.0)

	release, err := s.admitGeneration(ctx, "u1")
	if err != nil {
		t.Fatalf("admitGeneration err = %v, want nil", err)
	}
	release()
}

// TestAdmitGeneration_ExistingLimitGovernsOverConfig is the core of the
// snapshotted-limit design: an account created under an old config keeps its
// own limit even after the configured default changes. Here the account was
// seeded at a $1 ceiling and has spent it; a service now configured with a
// generous $10 default must still refuse, because the account's own stored
// limit — not the config — governs.
func TestAdmitGeneration_ExistingLimitGovernsOverConfig(t *testing.T) {
	store := quota.NewMemoryStore()
	ctx := context.Background()
	_ = store.Add(ctx, "u1", 1.0, 1.0) // account ceiling snapshotted at $1, fully spent
	s := newQuotaService(store, 10.0)

	if _, err := s.admitGeneration(ctx, "u1"); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("err = %v, want ErrOverBudget (stored limit must win over config)", err)
	}
}

// TestCharge_SeedsLimitForNewAccount verifies the configured default is
// snapshotted onto a brand-new account on its first charge.
func TestCharge_SeedsLimitForNewAccount(t *testing.T) {
	store := quota.NewMemoryStore()
	s := newQuotaService(store, 3.0)
	ctx := context.Background()

	if err := s.charge(ctx, "newbie", func() (float64, error) { return 0.2, nil }); err != nil {
		t.Fatalf("charge err = %v", err)
	}
	acct, err := store.Get(ctx, "newbie")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if acct.SpendLimitUSD != 3.0 {
		t.Fatalf("SpendLimitUSD = %v, want 3.0 (config seed on creation)", acct.SpendLimitUSD)
	}
}

func TestAdmitGeneration_InFlightRejectsSecond(t *testing.T) {
	store := quota.NewMemoryStore()
	s := newQuotaService(store, 1.0)
	ctx := context.Background()

	release, err := s.admitGeneration(ctx, "u1")
	if err != nil {
		t.Fatalf("first admitGeneration err = %v", err)
	}
	// Second concurrent admit for the same user is refused while the first holds.
	if _, err := s.admitGeneration(ctx, "u1"); !errors.Is(err, ErrGenerationInFlight) {
		t.Fatalf("second admitGeneration err = %v, want ErrGenerationInFlight", err)
	}
	release()
	// After release the user can be admitted again.
	release2, err := s.admitGeneration(ctx, "u1")
	if err != nil {
		t.Fatalf("admitGeneration after release err = %v", err)
	}
	release2()
}

func TestAdmitGeneration_OverBudgetReleasesLock(t *testing.T) {
	store := quota.NewMemoryStore()
	ctx := context.Background()
	_ = store.Add(ctx, "u1", 2.0, 1.0)
	s := newQuotaService(store, 1.0)

	// An over-budget rejection must not leave the in-flight lock held, or the
	// user would be permanently stuck at 409 instead of 402.
	if _, err := s.admitGeneration(ctx, "u1"); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("err = %v, want ErrOverBudget", err)
	}
	if _, err := s.admitGeneration(ctx, "u1"); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("second err = %v, want ErrOverBudget (lock leaked → got in-flight?)", err)
	}
}

func TestAdmitGeneration_ReadFailureFailsOpen(t *testing.T) {
	s := newQuotaService(&failingStore{}, 1.0)
	release, err := s.admitGeneration(context.Background(), "u1")
	if err != nil {
		t.Fatalf("admitGeneration with failing read err = %v, want nil (fail-open)", err)
	}
	release()
}

func TestCharge_AddsCostOnSuccess(t *testing.T) {
	store := quota.NewMemoryStore()
	s := newQuotaService(store, 1.0)
	ctx := context.Background()

	err := s.charge(ctx, "u1", func() (float64, error) { return 0.25, nil })
	if err != nil {
		t.Fatalf("charge err = %v", err)
	}
	acct, _ := store.Get(ctx, "u1")
	if acct.SpentUSD != 0.25 {
		t.Fatalf("SpentUSD = %v, want 0.25", acct.SpentUSD)
	}
}

func TestCharge_NoChargeOnError(t *testing.T) {
	store := quota.NewMemoryStore()
	s := newQuotaService(store, 1.0)
	ctx := context.Background()
	genErr := errors.New("llm failed")

	err := s.charge(ctx, "u1", func() (float64, error) { return 0.25, genErr })
	if !errors.Is(err, genErr) {
		t.Fatalf("charge err = %v, want genErr", err)
	}
	if _, err := store.Get(ctx, "u1"); !errors.Is(err, quota.ErrNotFound) {
		t.Fatalf("failed generation was charged: account exists (err=%v)", err)
	}
}

func TestCharge_WriteFailureFailsOpen(t *testing.T) {
	fs := &failingStore{}
	s := newQuotaService(fs, 1.0)

	// A store write failure must not surface to the caller — the generation
	// already succeeded and is paid for; the lost deduction is only logged.
	err := s.charge(context.Background(), "u1", func() (float64, error) { return 0.25, nil })
	if err != nil {
		t.Fatalf("charge err = %v, want nil (fail-open on write)", err)
	}
	if fs.addCalls != 1 {
		t.Fatalf("Add called %d times, want 1", fs.addCalls)
	}
}
