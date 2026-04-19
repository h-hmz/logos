package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/philosopher"
)

// testServer builds a Server and chi router with a fixed philosopher roster
// and model list, suitable for handler-level tests that don't invoke the ADK
// runner. Call once per test; config.Load is process-global so repeated calls
// are safe but must pass a consistent models.yaml.
func testServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	phils := []philosopher.Data{
		{Name: "Socrates", School: "Classical", Identity: "id-a", KeyThinkers: "k"},
		{Name: "Kant", School: "German Idealism", Identity: "id-b", KeyThinkers: "k"},
	}
	models := []config.ModelEntry{{ID: "test-model", Name: "Test Model"}}
	seedConfig(t, models)
	repo := debate.NewMemoryRepo()
	philMap := map[string]philosopher.Data{phils[0].Name: phils[0], phils[1].Name: phils[1]}
	s := &Server{
		philosophers: phils,
		philMap:      philMap,
		models:       models,
		repo:         repo,
		svc:          debate.NewService(repo, philMap),
	}
	return s, New(phils, models, repo)
}

func seedConfig(t *testing.T, models []config.ModelEntry) {
	t.Helper()
	dir := t.TempDir()
	y := "models:\n"
	for _, m := range models {
		y += "  - id: " + m.ID + "\n    name: " + m.Name + "\n"
	}
	path := filepath.Join(dir, "models.yaml")
	if err := os.WriteFile(path, []byte(y), 0o600); err != nil {
		t.Fatalf("write models.yaml: %v", err)
	}
	if err := config.Load(filepath.Join(dir, ".env.missing"), path); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
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

func TestListModels(t *testing.T) {
	_, h := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got []config.ModelEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "test-model" {
		t.Fatalf("unexpected models: %+v", got)
	}
}

func TestListPhilosophers(t *testing.T) {
	_, h := testServer(t)
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
	_, h := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/debates", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestCreateDebate_ValidationErrors(t *testing.T) {
	_, h := testServer(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"unknown philosopher", map[string]any{"topic": "t", "debater_a": "Ghost", "debater_b": "Kant", "model": "test-model"}},
		{"same debater", map[string]any{"topic": "t", "debater_a": "Kant", "debater_b": "Kant", "model": "test-model"}},
		{"empty topic", map[string]any{"topic": "", "debater_a": "Socrates", "debater_b": "Kant", "model": "test-model"}},
		{"bad rounds", map[string]any{"topic": "t", "debater_a": "Socrates", "debater_b": "Kant", "num_rounds": 99, "model": "test-model"}},
		{"bad model", map[string]any{"topic": "t", "debater_a": "Socrates", "debater_b": "Kant", "model": "mystery"}},
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
	_, h := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/debates/nope/next", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestNextTurn_AlreadyComplete(t *testing.T) {
	s, _ := testServer(t)
	p := &debate.Persisted{
		ID:           "d1",
		DebaterAName: s.philosophers[0].Name,
		DebaterBName: s.philosophers[1].Name,
		NumRounds:    1,
		ModelID:      "test-model",
		MaxWords:     100,
		Current:      debate.TotalSteps(1),
	}
	if err := s.repo.Create(context.Background(), p); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}

	r := chiReq(http.MethodPost, "/debates/d1/next", nil, map[string]string{"debateID": "d1"})
	rr := httptest.NewRecorder()
	s.nextTurn(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestInterject_NotFound(t *testing.T) {
	_, h := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/debates/nope/interject", strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestInterject_ValidationErrors(t *testing.T) {
	s, _ := testServer(t)
	phils := s.philosophers
	p := &debate.Persisted{
		ID:           "d1",
		DebaterAName: phils[0].Name,
		DebaterBName: phils[1].Name,
		NumRounds:    1,
		ModelID:      "test-model",
		MaxWords:     100,
		Current:      1, // before {a}_r1
	}
	if err := s.repo.Create(context.Background(), p); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}

	long := strings.Repeat("x", debate.InterjectionMaxLen+1)
	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"invalid JSON", "not-json", http.StatusBadRequest},
		{"empty text", `{"text":"  "}`, http.StatusBadRequest},
		{"too long", `{"text":"` + long + `"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := chiReq(http.MethodPost, "/debates/d1/interject", strings.NewReader(c.body), map[string]string{"debateID": "d1"})
			rr := httptest.NewRecorder()
			s.interject(rr, r)
			if rr.Code != c.status {
				t.Fatalf("status = %d, want %d — body: %s", rr.Code, c.status, rr.Body.String())
			}
		})
	}
}

func TestInterject_BeforeModerator(t *testing.T) {
	s, _ := testServer(t)
	p := &debate.Persisted{
		ID:           "d1",
		DebaterAName: s.philosophers[0].Name,
		DebaterBName: s.philosophers[1].Name,
		NumRounds:    1,
		ModelID:      "test-model",
		MaxWords:     100,
		Current:      0,
	}
	if err := s.repo.Create(context.Background(), p); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}
	r := chiReq(http.MethodPost, "/debates/d1/interject", strings.NewReader(`{"text":"hi"}`), map[string]string{"debateID": "d1"})
	rr := httptest.NewRecorder()
	s.interject(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(strings.ToLower(rr.Body.String()), "moderator") {
		t.Fatalf("error should mention moderator: %s", rr.Body.String())
	}
}

func TestInterject_WrongStepKind(t *testing.T) {
	s, _ := testServer(t)
	// With NumRounds=1, step 3 is critic_feedback — interject before it should 400.
	p := &debate.Persisted{
		ID:           "d1",
		DebaterAName: s.philosophers[0].Name,
		DebaterBName: s.philosophers[1].Name,
		NumRounds:    1,
		ModelID:      "test-model",
		MaxWords:     100,
		Current:      3,
	}
	if err := s.repo.Create(context.Background(), p); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}
	r := chiReq(http.MethodPost, "/debates/d1/interject", strings.NewReader(`{"text":"hi"}`), map[string]string{"debateID": "d1"})
	rr := httptest.NewRecorder()
	s.interject(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// TestRequestBodyClosed verifies the fix for issue #1: handlers must close
// r.Body after reading. Regression guard.
func TestRequestBodyClosed(t *testing.T) {
	_, h := testServer(t)
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

// ── Pure-function tests ─────────────────────────────────────────────────────

func TestParseTopicList(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"a\nb\nc", []string{"a", "b", "c"}},
		{"1. first\n2) second\n- third\n* fourth\n• fifth", []string{"first", "second", "third", "fourth", "fifth"}},
		{"\"quoted\"\n'apostrophes'", []string{"quoted", "apostrophes"}},
		{"  \n\nonly one\n\n", []string{"only one"}},
	}
	for _, c := range cases {
		got := parseTopicList(c.raw)
		if len(got) != len(c.want) {
			t.Fatalf("parseTopicList(%q) = %v, want %v", c.raw, got, c.want)
		}
		for i, g := range got {
			if g != c.want[i] {
				t.Fatalf("parseTopicList(%q)[%d] = %q, want %q", c.raw, i, g, c.want[i])
			}
		}
	}
}

