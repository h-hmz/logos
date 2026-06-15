package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/h-hmz/logos/internal/auth"
	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/philosopher"
	"github.com/h-hmz/logos/internal/quota"
)

const devUID = "dev-local" // UID returned by auth.NewNoopVerifier

// quotaTestServer builds a quota-enabled router with the given default limit.
// The quota store is returned so tests can seed spend; the wired Server lives
// inside the router. The noop verifier yields UID "dev-local" (see devUID), so
// seed and assert against that UID. The handler injects a bearer token so the
// noop verifier admits the request.
func quotaTestServer(t *testing.T, limit float64) (*quota.MemoryStore, http.Handler) {
	t.Helper()
	phils := []philosopher.Data{
		{Name: "Socrates", School: "Classical", Identity: "id-a", KeyThinkers: "k"},
		{Name: "Kant", School: "German Idealism", Identity: "id-b", KeyThinkers: "k"},
	}
	t.Setenv("MODEL", "test-model")
	if err := config.Load(""); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store := quota.NewMemoryStore()
	deps := &QuotaDeps{
		Store:           store,
		DefaultLimitUSD: limit,
		InFlight:        quota.NewInFlight(),
	}
	router := New(phils, debate.NewMemoryRepo(), nil, auth.NewNoopVerifier(), auth.ParseAllowlist("*"), nil, deps)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer test")
		}
		router.ServeHTTP(w, r)
	})
	return store, handler
}

func TestGetQuota_FreshUser(t *testing.T) {
	_, h := quotaTestServer(t, 1.0)
	req := httptest.NewRequest(http.MethodGet, "/me/quota", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", rr.Code, rr.Body.String())
	}
	var got quotaResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LimitUSD != 1.0 || got.SpentUSD != 0 || got.RemainingUSD != 1.0 {
		t.Fatalf("got %+v, want {1, 0, 1}", got)
	}
}

func TestGetQuota_ReflectsSpend(t *testing.T) {
	store, h := quotaTestServer(t, 1.0)
	_ = store.Add(context.Background(), devUID, 0.75, 1.0)

	req := httptest.NewRequest(http.MethodGet, "/me/quota", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var got quotaResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.SpentUSD != 0.75 {
		t.Fatalf("SpentUSD = %v, want 0.75", got.SpentUSD)
	}
	if got.RemainingUSD < 0.249 || got.RemainingUSD > 0.251 {
		t.Fatalf("RemainingUSD = %v, want ~0.25", got.RemainingUSD)
	}
}

func TestGetQuota_RemainingClampedAtZero(t *testing.T) {
	store, h := quotaTestServer(t, 1.0)
	_ = store.Add(context.Background(), devUID, 1.5, 1.0) // overshoot past limit

	req := httptest.NewRequest(http.MethodGet, "/me/quota", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var got quotaResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.RemainingUSD != 0 {
		t.Fatalf("RemainingUSD = %v, want 0 (clamped)", got.RemainingUSD)
	}
}

func TestGetQuota_DisabledReportsUnlimited(t *testing.T) {
	phils := []philosopher.Data{{Name: "Socrates", School: "Classical", Identity: "x", KeyThinkers: "k"}}
	t.Setenv("MODEL", "test-model")
	if err := config.Load(""); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	router := New(phils, debate.NewMemoryRepo(), nil, auth.NewNoopVerifier(), auth.ParseAllowlist("*"), nil, nil)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer test")
		router.ServeHTTP(w, r)
	})
	req := httptest.NewRequest(http.MethodGet, "/me/quota", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got quotaResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.LimitUSD >= 0 {
		t.Fatalf("LimitUSD = %v, want negative (unlimited sentinel)", got.LimitUSD)
	}
}

// TestNextTurn_OverBudget verifies an exhausted user is refused with 402 before
// any LLM call. Spend is seeded at the limit so the quota gate trips first.
func TestNextTurn_OverBudget(t *testing.T) {
	store, h := quotaTestServer(t, 1.0)
	_ = store.Add(context.Background(), devUID, 1.0, 1.0) // at limit

	req := httptest.NewRequest(http.MethodPost, "/debates/whatever/next", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 — body: %s", rr.Code, rr.Body.String())
	}
}
