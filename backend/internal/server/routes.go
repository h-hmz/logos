// Package server wires HTTP endpoints for the debate service. Handlers are
// split across files by functional domain (debate, tts, philosophers); this
// file owns only routing and shared transport helpers.
package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/h-hmz/logos/internal/auth"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/philosopher"
	"github.com/h-hmz/logos/internal/quota"
	"github.com/h-hmz/logos/internal/tts"
)

// Server holds the shared state for the HTTP handlers. Debate orchestration
// lives in debate.Service; this struct carries the transport-adjacent pieces
// (catalog for responses, TTS resolution).
type Server struct {
	philosophers []philosopher.Data
	svc          *debate.Service
	narrations   tts.NarrationStore
}

// QuotaDeps carries the per-user quota dependencies, required by New (quota is
// always enforced; ADR 0009). DefaultLimitUSD is the ceiling seeded onto a
// brand-new account.
type QuotaDeps struct {
	Store           quota.Store
	DefaultLimitUSD float64
	InFlight        *quota.InFlight
}

// New creates a chi router with all endpoints registered. The verifier
// authenticates callers on protected routes and allow restricts them to
// whitelisted emails/domains; pass auth.NewNoopVerifier() and an allow-all
// allowlist (auth.ParseAllowlist("*")) for local dev/tests. corsOrigins is the
// list of browser origins permitted by CORS; pass nil/empty to default to the
// local Vite dev origin. quotaDeps carries the required per-user quota
// dependencies (quota is always enforced). narrations persists synthesized audio
// for replay; it is only reached after the TTS-enabled check, so pass nil when
// TTS is disabled.
func New(philosophers []philosopher.Data, repo debate.Repository, narrations tts.NarrationStore, verifier auth.Verifier, allow auth.Allowlist, corsOrigins []string, quotaDeps *QuotaDeps) *chi.Mux {
	philMap := make(map[string]philosopher.Data, len(philosophers))
	for _, p := range philosophers {
		philMap[p.Name] = p
	}
	svc := debate.NewService(repo, philMap, debate.QuotaConfig{
		Store:           quotaDeps.Store,
		DefaultLimitUSD: quotaDeps.DefaultLimitUSD,
		InFlight:        quotaDeps.InFlight,
	})
	s := &Server{
		philosophers: philosophers,
		svc:          svc,
		narrations:   narrations,
	}

	r := chi.NewRouter()
	r.Use(requestIDMiddleware)
	r.Use(traceContextMiddleware)
	r.Use(recoverMiddleware)
	// Default to the local Vite dev origin when no origins are configured so
	// local dev and tests work without setting CORS_ALLOWED_ORIGINS.
	if len(corsOrigins) == 0 {
		corsOrigins = []string{"http://localhost:5173"}
	}
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: corsOrigins,
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-Request-Id"},
		ExposedHeaders: []string{"X-Request-Id"},
	}))

	// Public routes:
	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.healthz)

	// Protected routes:
	r.Group(func(pr chi.Router) {
		pr.Use(authMiddleware(verifier, allow))
		pr.Get("/philosophers", s.listPhilosophers)
		pr.Get("/me/quota", s.getQuota)
		pr.Post("/debates", s.createDebate)
		// Per-debate routes share sessionIDMiddleware so the slog handler
		// can stamp session_id on every log line. Chained per-route via
		// With(...) instead of a Route() sub-tree to keep URL semantics
		// (no trailing-slash sensitivity) identical to the previous flat
		// registration.
		dr := pr.With(sessionIDMiddleware)
		dr.Get("/debates/{debateID}", s.getDebate)
		dr.Post("/debates/{debateID}/next", s.nextTurn)
		dr.Post("/debates/{debateID}/retry", s.retryTurn)
		dr.Post("/debates/{debateID}/tutor", s.tutorExplain)
		dr.Post("/debates/{debateID}/tts", s.synthesizeStep)
	})

	return r
}

// healthz is a liveness/readiness probe.
func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// ── JSON helpers ────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"detail": msg})
}
