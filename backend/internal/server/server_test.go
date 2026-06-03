package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/h-hmz/logos/internal/auth"
	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/philosopher"
)

// testServer builds a Server and chi router with a fixed philosopher roster,
// suitable for handler-level tests that don't invoke the ADK runner. The repo
// is returned so tests can seed fixtures without reaching through Server.
func testServer(t *testing.T) (*Server, *debate.MemoryRepo, http.Handler) {
	t.Helper()
	phils := []philosopher.Data{
		{Name: "Socrates", School: "Classical", Identity: "id-a", KeyThinkers: "k"},
		{Name: "Kant", School: "German Idealism", Identity: "id-b", KeyThinkers: "k"},
	}
	t.Setenv("MODEL", "test-model")
	if err := config.Load(""); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	repo := debate.NewMemoryRepo()
	philMap := map[string]philosopher.Data{phils[0].Name: phils[0], phils[1].Name: phils[1]}
	s := &Server{
		philosophers: phils,
		philMap:      philMap,
		svc:          debate.NewService(repo, philMap),
	}
	router := New(phils, repo, auth.NewNoopVerifier(), auth.ParseAllowlist("*"), nil, nil)
	// Existing handler tests predate auth; inject a bearer so the noop verifier
	// accepts them. Dedicated 401 coverage lives in auth_test.go.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer test")
		}
		router.ServeHTTP(w, r)
	})
	return s, repo, handler
}

// chiReq wraps an httptest request with a chi RouteContext carrying URL params,
// so handlers can be invoked directly without going through the router.
func chiReq(method, target string, body io.Reader, params map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, body)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// ── Handler tests ───────────────────────────────────────────────────────────

func TestListPhilosophers(t *testing.T) {
	_, _, h := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/philosophers", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d philosophers, want 2", len(got))
	}
	if _, leaked := got[0]["identity"]; leaked {
		t.Fatalf("identity should not be serialized in /philosophers response")
	}
}

func TestCreateDebate_InvalidJSON(t *testing.T) {
	_, _, h := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/debates", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestCreateDebate_ValidationErrors(t *testing.T) {
	_, _, h := testServer(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"unknown philosopher", map[string]any{"topic": "t", "debater_a": "Ghost", "debater_b": "Kant"}},
		{"same debater", map[string]any{"topic": "t", "debater_a": "Kant", "debater_b": "Kant"}},
		{"empty topic", map[string]any{"topic": "", "debater_a": "Socrates", "debater_b": "Kant"}},
		{"bad rounds", map[string]any{"topic": "t", "debater_a": "Socrates", "debater_b": "Kant", "num_rounds": 99}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/debates", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 — body: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestNextTurn_NotFound(t *testing.T) {
	_, _, h := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/debates/nope/next", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestNextTurn_AlreadyComplete(t *testing.T) {
	s, repo, _ := testServer(t)
	p := &debate.Persisted{
		ID:           "d1",
		DebaterAName: s.philosophers[0].Name,
		DebaterBName: s.philosophers[1].Name,
		NumRounds:    1,
		ModelID:      "test-model",
		MaxWords:     100,
		// A full set of step records marks the debate complete (progress is
		// derived from StepRecords length, not a stored counter).
		StepRecords: make([]debate.StepRecord, debate.TotalSteps(1)),
	}
	if err := repo.Create(context.Background(), p); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}

	r := chiReq(http.MethodPost, "/debates/d1/next", nil, map[string]string{"debateID": "d1"})
	rr := httptest.NewRecorder()
	s.nextTurn(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// TestCreateDebate_StampsOwner verifies the authenticated caller is recorded as
// the debate's owner. The noop verifier yields UID "dev-local".
func TestCreateDebate_StampsOwner(t *testing.T) {
	_, repo, h := testServer(t)
	body, _ := json.Marshal(map[string]any{
		"topic":     "free will",
		"debater_a": "Socrates",
		"debater_b": "Kant",
	})
	req := httptest.NewRequest(http.MethodPost, "/debates", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", rr.Code, rr.Body.String())
	}
	var resp createDebateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	p, err := repo.Get(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if p.OwnerUID != "dev-local" {
		t.Fatalf("OwnerUID = %q, want %q", p.OwnerUID, "dev-local")
	}
}

// TestNextTurn_NonOwnerForbidden verifies a caller who does not own a debate is
// refused with 403 when advancing it (reads stay open; only /next is gated).
func TestNextTurn_NonOwnerForbidden(t *testing.T) {
	s, repo, h := testServer(t)
	p := &debate.Persisted{
		ID:           "d1",
		OwnerUID:     "someone-else",
		DebaterAName: s.philosophers[0].Name,
		DebaterBName: s.philosophers[1].Name,
		NumRounds:    1,
		ModelID:      "test-model",
		MaxWords:     100,
	}
	if err := repo.Create(context.Background(), p); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}
	// Through the router the noop verifier yields UID "dev-local", not the owner.
	req := httptest.NewRequest(http.MethodPost, "/debates/d1/next", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — body: %s", rr.Code, rr.Body.String())
	}
}

// TestRequestBodyClosed verifies the fix for issue #1: handlers must close
// r.Body after reading. Regression guard.
func TestRequestBodyClosed(t *testing.T) {
	_, _, h := testServer(t)
	b := &trackedBody{Reader: strings.NewReader(`{}`)}
	req := httptest.NewRequest(http.MethodPost, "/debates", b)
	req.Body = b
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !b.closed {
		t.Fatalf("request body was not closed by handler")
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (t *trackedBody) Close() error { t.closed = true; return nil }

