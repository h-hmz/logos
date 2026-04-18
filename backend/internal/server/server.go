package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/philosopher"
)

// Server holds the shared state for the HTTP handlers.
type Server struct {
	philosophers []philosopher.Data
	philMap      map[string]philosopher.Data // name → data
	models       []config.ModelEntry
	store        *debate.Store
}

// New creates a chi router with all endpoints registered.
func New(philosophers []philosopher.Data, models []config.ModelEntry) *chi.Mux {
	s := &Server{
		philosophers: philosophers,
		philMap:      make(map[string]philosopher.Data, len(philosophers)),
		models:       models,
		store:        debate.NewStore(),
	}
	for _, p := range philosophers {
		s.philMap[p.Name] = p
	}

	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"http://localhost:5173"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type"},
	}))

	r.Get("/models", s.listModels)
	r.Get("/philosophers", s.listPhilosophers)
	r.Post("/debates", s.createDebate)
	r.Post("/debates/{debateID}/next", s.nextTurn)

	return r
}

// ── GET /models ─────────────────────────────────────────────────────────────

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.models)
}

// ── GET /philosophers ───────────────────────────────────────────────────────

type philosopherItem struct {
	Name     string `json:"name"`
	School   string `json:"school"`
	Era      string `json:"era,omitempty"`
	Dates    string `json:"dates,omitempty"`
	OneLiner string `json:"one_liner,omitempty"`
}

func (s *Server) listPhilosophers(w http.ResponseWriter, r *http.Request) {
	items := make([]philosopherItem, len(s.philosophers))
	for i, p := range s.philosophers {
		items[i] = philosopherItem{
			Name:     p.Name,
			School:   p.School,
			Era:      p.Era,
			Dates:    p.Dates,
			OneLiner: p.OneLiner,
		}
	}
	writeJSON(w, http.StatusOK, items)
}

// ── POST /debates ───────────────────────────────────────────────────────────

type createDebateRequest struct {
	Topic     string `json:"topic"`
	DebaterA  string `json:"debater_a"`
	DebaterB  string `json:"debater_b"`
	NumRounds int    `json:"num_rounds"`
	Model     string `json:"model"`
	MaxWords  int    `json:"max_words"`
}

type stepInfo struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type debaterInfo struct {
	Name   string `json:"name"`
	School string `json:"school"`
}

type createDebateResponse struct {
	ID         string      `json:"id"`
	Topic      string      `json:"topic"`
	DebaterA   debaterInfo `json:"debater_a"`
	DebaterB   debaterInfo `json:"debater_b"`
	Steps      []stepInfo  `json:"steps"`
	TotalSteps int         `json:"total_steps"`
}

func (s *Server) createDebate(w http.ResponseWriter, r *http.Request) {
	var req createDebateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Apply defaults
	if req.NumRounds == 0 {
		req.NumRounds = 2
	}
	if req.Model == "" {
		req.Model = config.DefaultModel()
	}
	if req.MaxWords == 0 {
		req.MaxWords = 250
	}

	// Validate
	a, okA := s.philMap[req.DebaterA]
	b, okB := s.philMap[req.DebaterB]
	if !okA || !okB {
		writeError(w, http.StatusBadRequest, "Unknown philosopher name")
		return
	}
	if req.DebaterA == req.DebaterB {
		writeError(w, http.StatusBadRequest, "Debaters must be different")
		return
	}
	if req.NumRounds < 1 || req.NumRounds > 5 {
		writeError(w, http.StatusBadRequest, "num_rounds must be between 1 and 5")
		return
	}
	if req.Topic == "" {
		writeError(w, http.StatusBadRequest, "Topic cannot be empty")
		return
	}
	if !config.ValidModel(req.Model) {
		writeError(w, http.StatusBadRequest, "Unknown model id")
		return
	}

	ctx := r.Context()
	steps, err := debate.BuildSteps(ctx, a, b, req.NumRounds, req.Model, req.MaxWords)
	if err != nil {
		log.Printf("BuildSteps error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to build debate pipeline")
		return
	}

	svc := session.InMemoryService()
	createResp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "logos",
		UserID:  "web",
		State:   map[string]any{"topic": req.Topic},
	})
	if err != nil {
		log.Printf("session create error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	debateID := uuid.NewString()
	ds := &debate.State{
		ID:             debateID,
		Topic:          req.Topic,
		DebaterA:       a,
		DebaterB:       b,
		Steps:          steps,
		SessionService: svc,
		SessionID:      createResp.Session.ID(),
	}
	s.store.Put(debateID, ds)

	resp := createDebateResponse{
		ID:         debateID,
		Topic:      req.Topic,
		DebaterA:   debaterInfo{Name: a.Name, School: a.School},
		DebaterB:   debaterInfo{Name: b.Name, School: b.School},
		TotalSteps: len(steps),
	}
	for _, step := range steps {
		resp.Steps = append(resp.Steps, stepInfo{Key: step.Key, Label: step.Label})
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── POST /debates/{debateID}/next ───────────────────────────────────────────

type turnResult struct {
	Key               string `json:"key"`
	Label             string `json:"label"`
	Author            string `json:"author"`
	Role              string `json:"role"`
	Content           string `json:"content"`
	Structured        any    `json:"structured,omitempty"`
	StepIndex         int    `json:"step_index"`
	TotalSteps        int    `json:"total_steps"`
	Done              bool   `json:"done"`
	TurnInputTokens   int    `json:"turn_input_tokens"`
	TurnOutputTokens  int    `json:"turn_output_tokens"`
	TotalInputTokens  int    `json:"total_input_tokens"`
	TotalOutputTokens int    `json:"total_output_tokens"`
}

// roleForStep maps a pipeline step key to the frontend-facing role label.
// Matches the derivation in frontend/src/step-role.ts.
func roleForStep(key string, a, b philosopher.Data) string {
	switch key {
	case "moderator_intro":
		return "moderator"
	case "critic_feedback":
		return "critic"
	case "final_verdict":
		return "judge"
	}
	if strings.HasPrefix(key, philosopher.Key(a.Name)) {
		return "a"
	}
	if strings.HasPrefix(key, philosopher.Key(b.Name)) {
		return "b"
	}
	return "moderator"
}

// extractJSON strips common wrappers (```json … ``` fences, leading prose)
// and returns the substring from the first '{' to the matching '}'. Returns
// the empty string if no balanced object is found.
func extractJSON(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "```") {
		// drop the opening fence line
		if nl := strings.IndexByte(t, '\n'); nl >= 0 {
			t = t[nl+1:]
		}
		if end := strings.LastIndex(t, "```"); end >= 0 {
			t = t[:end]
		}
	}
	start := strings.IndexByte(t, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(t); i++ {
		c := t[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return t[start : i+1]
			}
		}
	}
	return ""
}

func (s *Server) nextTurn(w http.ResponseWriter, r *http.Request) {
	debateID := chi.URLParam(r, "debateID")
	ds, ok := s.store.Get(debateID)
	if !ok {
		writeError(w, http.StatusNotFound, "Debate not found")
		return
	}

	// Serialize concurrent /next calls on the same debate
	ds.Mu.Lock()
	defer ds.Mu.Unlock()

	if ds.Current >= len(ds.Steps) {
		writeError(w, http.StatusBadRequest, "Debate is already complete")
		return
	}

	step := ds.Steps[ds.Current]
	ctx := r.Context()

	rn, err := runner.New(runner.Config{
		Agent:          step.Agent,
		AppName:        "logos",
		SessionService: ds.SessionService,
	})
	if err != nil {
		log.Printf("runner create error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create runner")
		return
	}

	msg := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{genai.NewPartFromText("continue")},
	}

	// Check if client wants SSE streaming
	sse, isSSE := false, false
	var sw *sseWriter
	if wantsSSE(r) {
		sw, isSSE = newSSEWriter(w)
		if isSSE {
			sse = true
		}
	}

	var content string
	var turnInput, turnOutput int
	for event, err := range rn.Run(ctx, "web", ds.SessionID, msg, agent.RunConfig{}) {
		if err != nil {
			errMsg := fmt.Sprintf("runner error: %v", err)
			log.Print(errMsg)
			if sse {
				sw.SendError(errMsg)
			} else {
				writeError(w, http.StatusInternalServerError, errMsg)
			}
			return
		}

		if event.UsageMetadata != nil {
			if event.UsageMetadata.PromptTokenCount > 0 {
				turnInput = int(event.UsageMetadata.PromptTokenCount)
			}
			if event.UsageMetadata.CandidatesTokenCount > 0 {
				turnOutput = int(event.UsageMetadata.CandidatesTokenCount)
			}
		}

		if event.Content != nil && len(event.Content.Parts) > 0 {
			for _, part := range event.Content.Parts {
				if part.Text != "" {
					if sse && event.Partial {
						sw.SendToken(part.Text)
					}
					content = part.Text
				}
			}
		}
	}

	ds.TotalInputTokens += turnInput
	ds.TotalOutputTokens += turnOutput
	stepIndex := ds.Current
	ds.Current++

	role := roleForStep(step.Key, ds.DebaterA, ds.DebaterB)

	var structured any
	if step.Key == "critic_feedback" || step.Key == "final_verdict" {
		if raw := extractJSON(content); raw != "" {
			var parsed any
			if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
				structured = parsed
			} else {
				log.Printf("failed to parse %s JSON: %v", step.Key, err)
			}
		}
	}

	result := turnResult{
		Key:               step.Key,
		Label:             step.Label,
		Author:            step.Agent.Name(),
		Role:              role,
		Content:           content,
		Structured:        structured,
		StepIndex:         stepIndex,
		TotalSteps:        len(ds.Steps),
		Done:              ds.Current >= len(ds.Steps),
		TurnInputTokens:   turnInput,
		TurnOutputTokens:  turnOutput,
		TotalInputTokens:  ds.TotalInputTokens,
		TotalOutputTokens: ds.TotalOutputTokens,
	}

	if sse {
		sw.SendDone(result)
	} else {
		writeJSON(w, http.StatusOK, result)
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

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
