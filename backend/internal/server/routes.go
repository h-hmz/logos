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
	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/philosopher"
)

// Server holds the shared state for the HTTP handlers. Debate orchestration
// lives in debate.Service; this struct carries the transport-adjacent pieces
// (catalog for responses, model list, TTS resolution).
type Server struct {
	philosophers []philosopher.Data
	philMap      map[string]philosopher.Data
	models       []config.ModelEntry
	repo         debate.Repository
	svc          *debate.Service
}

// New creates a chi router with all endpoints registered. The verifier gates
// protected routes; pass auth.NewNoopVerifier() for local dev/tests.
func New(philosophers []philosopher.Data, models []config.ModelEntry, repo debate.Repository, verifier auth.Verifier) *chi.Mux {
	philMap := make(map[string]philosopher.Data, len(philosophers))
	for _, p := range philosophers {
		philMap[p.Name] = p
	}
	s := &Server{
		philosophers: philosophers,
		philMap:      philMap,
		models:       models,
		repo:         repo,
		svc:          debate.NewService(repo, philMap),
	}

	r := chi.NewRouter()
	r.Use(requestIDMiddleware)
	r.Use(accessLogMiddleware)
	r.Use(recoverMiddleware)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"http://localhost:5173"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-Request-Id"},
		ExposedHeaders: []string{"X-Request-Id"},
	}))

	// Public routes:
	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.healthz)
	r.Get("/models", s.listModels)
	r.Get("/philosophers", s.listPhilosophers)

	// Protected routes:
	r.Group(func(pr chi.Router) {
		pr.Use(authMiddleware(verifier))
		pr.Post("/debates", s.createDebate)
		pr.Post("/debates/{debateID}/next", s.nextTurn)
		pr.Post("/debates/{debateID}/interject", s.interject)
		pr.Post("/debates/{debateID}/tutor", s.tutorExplain)
		pr.Post("/debates/{debateID}/tts", s.synthesizeStep)
		pr.Post("/suggest-topic", s.suggestTopic)
	})

	return r
}

// healthz is a liveness/readiness probe.
func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// debaters resolves the philosopher.Data pair referenced by a Persisted record.
func (s *Server) debaters(p *debate.Persisted) (a, b philosopher.Data, ok bool) {
	var okA, okB bool
	a, okA = s.philMap[p.DebaterAName]
	b, okB = s.philMap[p.DebaterBName]
	return a, b, okA && okB
}

// ── GET /models ─────────────────────────────────────────────────────────────

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.models)
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
