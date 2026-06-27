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
	"github.com/h-hmz/logos/internal/quota"
	"github.com/h-hmz/logos/internal/tts"
)

// testQuotaDeps returns quota deps with a fresh in-memory store and a budget high
// enough that handler tests never hit the limit. Quota is always enforced now
// (ADR 0009), so every server.New / debate.NewService caller must supply it.
func testQuotaDeps() *QuotaDeps {
	return &QuotaDeps{Store: quota.NewMemoryStore(), DefaultLimitUSD: 1e9, InFlight: quota.NewInFlight()}
}

// testServer builds a Server and chi router with a fixed philosopher roster,
// suitable for handler-level tests that don't invoke the ADK runner. The repo
// is returned so tests can seed fixtures without reaching through Server.
func testServer(t *testing.T) (*Server, *debate.MemoryRepo, http.Handler) {
	t.Helper()
	phils := []philosopher.Data{
		{Name: "Socrates", School: "Classical", Identity: "id-a", KeyThinkers: "k"},
		{Name: "Kant", School: "German Idealism", Identity: "id-b", KeyThinkers: "k"},
	}
	t.Setenv("QUOTA_USD_LIMIT", "1")
	t.Setenv("TTS_USD_PER_1M_AUDIO_TOKENS", "20")
	t.Setenv("TTS_USD_PER_GB_EGRESS", "0.12")
	if err := config.Load(""); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	repo := debate.NewMemoryRepo()
	philMap := map[string]philosopher.Data{phils[0].Name: phils[0], phils[1].Name: phils[1]}
	deps := testQuotaDeps()
	s := &Server{
		philosophers: phils,
		svc:          debate.NewService(repo, philMap, debate.QuotaConfig{Store: deps.Store, DefaultLimitUSD: deps.DefaultLimitUSD, InFlight: deps.InFlight}),
	}
	router := New(phils, repo, nil, auth.NewNoopVerifier(), auth.ParseAllowlist("*"), nil, deps)
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

// TestGetDebate_ReturnsPerTurnUsage verifies a completed step's per-turn input/
// output tokens and cost surface in GET /debates/{id}, so the live transcript
// can label each turn. Regression guard: GetSession once dropped the per-turn
// counts, leaving only running totals.
func TestGetDebate_ReturnsPerTurnUsage(t *testing.T) {
	s, repo, h := testServer(t)
	p := &debate.Persisted{
		ID:           "d1",
		OwnerUID:     "dev-local",
		DebaterAName: s.philosophers[0].Name,
		DebaterBName: s.philosophers[1].Name,
		NumRounds:    1,
		MaxWords:     100,
		// One completed step (the moderator intro) carrying provider-billed usage.
		StepRecords: []debate.StepRecord{
			{Key: "moderator_intro", Content: "Welcome.", InputTokens: 321, OutputTokens: 705, CostUSD: 0.0042},
		},
	}
	if err := repo.Create(context.Background(), p); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/debates/d1", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", rr.Code, rr.Body.String())
	}
	var got getDebateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	turn, ok := got.Turns[0]
	if !ok {
		t.Fatalf("turns[0] missing in response: %v", got.Turns)
	}
	if turn.TurnInputTokens != 321 || turn.TurnOutputTokens != 705 {
		t.Errorf("per-turn tokens = (%d, %d), want (321, 705)", turn.TurnInputTokens, turn.TurnOutputTokens)
	}
	if turn.TurnCostUSD != 0.0042 {
		t.Errorf("TurnCostUSD = %v, want 0.0042", turn.TurnCostUSD)
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

// TestRetryTurn_Gates exercises the retry endpoint's pre-generation rejections
// through the router, verifying each maps to the right status: 404 for an
// unknown debate, 403 for a non-owner, 400 for an empty debate and a missing
// step_key, and 409 when the named step is not the current last record. All
// return before any model call. The noop verifier yields UID "dev-local".
func TestRetryTurn_Gates(t *testing.T) {
	seedRetry := func(t *testing.T, repo *debate.MemoryRepo, owner string, records ...debate.StepRecord) {
		t.Helper()
		err := repo.Create(context.Background(), &debate.Persisted{
			ID: "d1", OwnerUID: owner,
			DebaterAName: "Socrates", DebaterBName: "Kant",
			NumRounds: 1, MaxWords: 100, StepRecords: records,
		})
		if err != nil {
			t.Fatalf("repo.Create: %v", err)
		}
	}
	post := func(t *testing.T, h http.Handler, target, body string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	t.Run("unknown debate is 404", func(t *testing.T) {
		_, _, h := testServer(t)
		if code := post(t, h, "/debates/nope/retry", `{"step_key":"moderator_intro"}`); code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", code)
		}
	})

	t.Run("non-owner is 403", func(t *testing.T) {
		_, repo, h := testServer(t)
		seedRetry(t, repo, "someone-else", debate.StepRecord{Key: "moderator_intro", Content: "Welcome."})
		if code := post(t, h, "/debates/d1/retry", `{"step_key":"moderator_intro"}`); code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
	})

	t.Run("empty debate is 400", func(t *testing.T) {
		_, repo, h := testServer(t)
		seedRetry(t, repo, "dev-local") // no completed steps
		if code := post(t, h, "/debates/d1/retry", `{"step_key":"moderator_intro"}`); code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", code)
		}
	})

	t.Run("missing step_key is 400", func(t *testing.T) {
		_, repo, h := testServer(t)
		seedRetry(t, repo, "dev-local", debate.StepRecord{Key: "moderator_intro", Content: "Welcome."})
		if code := post(t, h, "/debates/d1/retry", `{}`); code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", code)
		}
	})

	t.Run("non-last step is 409", func(t *testing.T) {
		_, repo, h := testServer(t)
		seedRetry(t, repo, "dev-local",
			debate.StepRecord{Key: "moderator_intro", Content: "Welcome."},
			debate.StepRecord{Key: "socrates_r1", Content: "My opening."},
		)
		if code := post(t, h, "/debates/d1/retry", `{"step_key":"moderator_intro"}`); code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", code)
		}
	})
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

// TestNarrationsStatus verifies GET /debates/{id}/narrations reports cached=true
// for a debater step whose narration is already stored and cached=false for one
// that is not, while omitting non-narratable steps (the moderator). It confirms
// the probe derives the same key the synthesis path stores under, via the shared
// narrationKey helper.
func TestNarrationsStatus(t *testing.T) {
	t.Setenv("TTS_ENABLED", "TRUE")
	s, repo, _ := testServer(t) // sets a non-zero TTS synthesis rate, so cached steps report a cost
	narr := tts.NewMemoryNarrationStore()
	s.narrations = narr

	const kantText = "The categorical imperative binds us."
	err := repo.Create(context.Background(), &debate.Persisted{
		ID: "d1", OwnerUID: "dev-local",
		DebaterAName: "Socrates", DebaterBName: "Kant",
		NumRounds: 1, MaxWords: 100,
		StepRecords: []debate.StepRecord{
			{Key: "moderator_intro", Content: "Welcome."},     // not narratable
			{Key: "socrates_r1", Content: "I know nothing."},   // narratable, uncached
			{Key: "kant_r1", Content: kantText},                // narratable, cached below
		},
	})
	if err != nil {
		t.Fatalf("repo.Create: %v", err)
	}

	// Pre-synthesize the Kant turn: store a narration under the exact key the
	// handler will derive for it.
	voice, style, _ := tts.VoiceForStepKey("kant_r1", s.philosophers)
	key := tts.NarrationKey("d1", stripParentheticals(kantText), voice, style, config.TTSModel())
	if err := narr.Put(context.Background(), key, []byte("pcm")); err != nil {
		t.Fatalf("narr.Put: %v", err)
	}

	req := chiReq(http.MethodGet, "/debates/d1/narrations", nil, map[string]string{"debateID": "d1"})
	rr := httptest.NewRecorder()
	s.narrationsStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body: %s", rr.Code, rr.Body.String())
	}

	var got narrationsStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Narrations["kant_r1"].Cached {
		t.Errorf("kant_r1 cached = false, want true (narration was stored) — got: %v", got.Narrations)
	}
	if info, ok := got.Narrations["socrates_r1"]; !ok || info.Cached {
		t.Errorf("socrates_r1 = (%+v, present=%v), want (cached=false, present=true)", info, ok)
	}
	if _, ok := got.Narrations["moderator_intro"]; ok {
		t.Errorf("moderator_intro should be omitted (not narratable) — got: %v", got.Narrations)
	}
	// kant_r1's narration is 3 bytes ("pcm"); its surfaced cost must be the
	// byte-derived synthesis figure (ADR 0013 §6), not zero or persisted.
	wantCost := tts.Pricing{USDPer1MAudioTokens: config.USDPer1MAudioTokens()}.SynthesisUSD(len("pcm"))
	if got := got.Narrations["kant_r1"].SynthesisCostUSD; got != wantCost {
		t.Errorf("kant_r1 synthesis_cost_usd = %v, want %v (derived from 3 stored bytes)", got, wantCost)
	}
	// An uncached step carries no cost — nothing is stored to measure.
	if got := got.Narrations["socrates_r1"].SynthesisCostUSD; got != 0 {
		t.Errorf("socrates_r1 synthesis_cost_usd = %v, want 0 (uncached)", got)
	}
}
