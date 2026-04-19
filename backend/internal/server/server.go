package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/modelcache"
	"github.com/h-hmz/logos/internal/philosopher"
	"github.com/h-hmz/logos/internal/tts"
)

// Non-debater role voices, keyed by provider then role. Falls back to the
// "gemini" entry when the active provider has no override.
var roleVoices = map[string]map[string]string{
	"gemini": {
		"moderator": "Charon",
		"critic":    "Puck",
		"judge":     "Orus",
		"fallback":  "Kore",
	},
	"elevenlabs": {
		"moderator": "JBFqnCBsd6RMkjVDRZzb", // George — warm, authoritative
		"critic":    "onwK4e9ZLuTAKqWW03F9", // Daniel — sharp, incisive
		"judge":     "nPczCjzI2devNBz1zQrb", // Brian — measured, grave
		"fallback":  "pNInz6obpgDQGcFmaJgB", // Adam — neutral default
	},
	"openai": {
		"moderator": "fable",
		"critic":    "ash",
		"judge":     "onyx",
		"fallback":  "alloy",
	},
}

func roleVoice(provider, role string) string {
	if m, ok := roleVoices[provider]; ok {
		if v := m[role]; v != "" {
			return v
		}
	}
	return roleVoices["gemini"][role]
}

// philosopherVoice picks the provider-specific voice from Data.TTSVoices,
// or falls back to the provider's role fallback voice when no entry exists.
func philosopherVoice(provider string, d philosopher.Data) string {
	if v, ok := d.TTSVoices[provider]; ok && v != "" {
		return v
	}
	return roleVoice(provider, "fallback")
}

// voiceForStep resolves the TTS voice and style for the given step role.
// Returns ("", "", false) when the step should not be synthesized.
func voiceForStep(role string, a, b philosopher.Data) (voice, style string, ok bool) {
	provider := strings.ToLower(config.TTSProvider())
	switch role {
	case "a":
		return philosopherVoice(provider, a), a.TTSStyle, true
	case "b":
		return philosopherVoice(provider, b), b.TTSStyle, true
	case "moderator":
		return roleVoice(provider, "moderator"), "Speak with gravitas, like a seasoned debate moderator.", true
	case "critic":
		return roleVoice(provider, "critic"), "Speak incisively, like a sharp academic critic.", true
	case "judge":
		return roleVoice(provider, "judge"), "Speak with measured, judicial authority.", true
	}
	return "", "", false
}

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

// New creates a chi router with all endpoints registered.
func New(philosophers []philosopher.Data, models []config.ModelEntry, repo debate.Repository) *chi.Mux {
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
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"http://localhost:5173"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type"},
	}))

	r.Get("/models", s.listModels)
	r.Get("/philosophers", s.listPhilosophers)
	r.Post("/debates", s.createDebate)
	r.Post("/debates/{debateID}/next", s.nextTurn)
	r.Post("/debates/{debateID}/interject", s.interject)
	r.Post("/debates/{debateID}/tts", s.synthesizeStep)
	r.Post("/suggest-topic", s.suggestTopic)

	return r
}

// debaters resolves the philosopher.Data pair referenced by a Persisted record.
func (s *Server) debaters(p *debate.Persisted) (a, b philosopher.Data, ok bool) {
	var okA, okB bool
	a, okA = s.philMap[p.DebaterAName]
	b, okB = s.philMap[p.DebaterBName]
	return a, b, okA && okB
}

// writeServiceError maps domain errors to HTTP responses.
func writeServiceError(w http.ResponseWriter, err error) {
	var ve *debate.ValidationError
	switch {
	case errors.Is(err, debate.ErrNotFound):
		writeError(w, http.StatusNotFound, "Debate not found")
	case errors.Is(err, debate.ErrVersionConflict):
		writeError(w, http.StatusConflict, "debate state changed concurrently; refresh and retry")
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, ve.Msg)
	default:
		log.Printf("service error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
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
	defer r.Body.Close()
	var req createDebateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	res, err := s.svc.CreateDebate(r.Context(), debate.CreateDebateParams{
		Topic:     req.Topic,
		DebaterA:  req.DebaterA,
		DebaterB:  req.DebaterB,
		NumRounds: req.NumRounds,
		Model:     req.Model,
		MaxWords:  req.MaxWords,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	resp := createDebateResponse{
		ID:         res.Debate.ID,
		Topic:      res.Debate.Topic,
		DebaterA:   debaterInfo{Name: res.DebaterA.Name, School: res.DebaterA.School},
		DebaterB:   debaterInfo{Name: res.DebaterB.Name, School: res.DebaterB.School},
		TotalSteps: len(res.Steps),
	}
	for _, sm := range res.Steps {
		resp.Steps = append(resp.Steps, stepInfo{Key: sm.Key, Label: sm.Label})
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

func (s *Server) nextTurn(w http.ResponseWriter, r *http.Request) {
	debateID := chi.URLParam(r, "debateID")

	// Decide transport mode before invoking the service so we can wire an
	// SSE token callback when the client negotiated streaming.
	var sw *sseWriter
	var sse bool
	if wantsSSE(r) {
		sw, sse = newSSEWriter(w)
	}

	var onToken func(string)
	if sse {
		onToken = sw.SendToken
	}

	res, err := s.svc.NextTurn(r.Context(), debateID, onToken)
	if err != nil {
		if sse {
			sw.SendError(serviceErrorMessage(err))
			return
		}
		writeServiceError(w, err)
		return
	}

	out := turnResult{
		Key:               res.Step.Key,
		Label:             res.Step.Label,
		Author:            res.AgentName,
		Role:              res.Role,
		Content:           res.Content,
		Structured:        res.Structured,
		StepIndex:         res.StepIndex,
		TotalSteps:        res.TotalSteps,
		Done:              res.Done,
		TurnInputTokens:   res.TurnInputTokens,
		TurnOutputTokens:  res.TurnOutputTokens,
		TotalInputTokens:  res.TotalInputTokens,
		TotalOutputTokens: res.TotalOutputTokens,
	}
	if sse {
		sw.SendDone(out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// serviceErrorMessage returns the user-facing message for a service error,
// used when the transport has already committed to SSE and can't emit an HTTP
// status code.
func serviceErrorMessage(err error) string {
	var ve *debate.ValidationError
	switch {
	case errors.Is(err, debate.ErrNotFound):
		return "Debate not found"
	case errors.Is(err, debate.ErrVersionConflict):
		return "debate state changed concurrently; refresh and retry"
	case errors.As(err, &ve):
		return ve.Msg
	default:
		log.Printf("service error (sse): %v", err)
		return "internal error"
	}
}

// ── POST /debates/{debateID}/tts ────────────────────────────────────────────

type ttsRequest struct {
	StepKey string `json:"step_key"`
	Text    string `json:"text"`
}

// synthesizeStep turns the given text into speech using the voice and style
// associated with the named step (resolved from the debate's debaters). The
// text is taken from the request so the client can replay arbitrary content
// it already has without the server storing turn outputs.
func (s *Server) synthesizeStep(w http.ResponseWriter, r *http.Request) {
	if !config.TTSEnabled() {
		writeError(w, http.StatusServiceUnavailable, "TTS is disabled on this server")
		return
	}

	debateID := chi.URLParam(r, "debateID")
	p, err := s.repo.Get(r.Context(), debateID)
	if errors.Is(err, debate.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Debate not found")
		return
	}
	if err != nil {
		log.Printf("repo get: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to load debate")
		return
	}
	a, b, ok := s.debaters(p)
	if !ok {
		writeError(w, http.StatusInternalServerError, "unknown philosopher in debate")
		return
	}

	defer r.Body.Close()
	var req ttsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "text is empty")
		return
	}
	if req.StepKey == "" {
		writeError(w, http.StatusBadRequest, "step_key is required")
		return
	}

	role := debate.RoleForStep(req.StepKey, a, b)
	voice, style, ok := voiceForStep(role, a, b)
	if !ok {
		writeError(w, http.StatusBadRequest, "step is not speakable")
		return
	}

	ctx := r.Context()
	client, err := tts.Shared(ctx)
	if err != nil || client == nil {
		log.Printf("tts: client unavailable: %v", err)
		writeError(w, http.StatusServiceUnavailable, "TTS client unavailable")
		return
	}
	// Prefer streaming when the provider supports it: first audio arrives in
	// 1-3s instead of waiting for the full synthesis.
	if streamer, ok := client.(tts.StreamSynthesizer); ok {
		headerSent := false
		onStart := func(rate int) {
			w.Header().Set("Content-Type", fmt.Sprintf("audio/L16;rate=%d;channels=1", rate))
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-TTS-Voice", voice)
			w.WriteHeader(http.StatusOK)
			headerSent = true
		}
		if err := streamer.SynthesizeStream(ctx, text, voice, style, onStart, w); err != nil {
			if !headerSent {
				log.Printf("tts: stream step %s: %v", req.StepKey, err)
				writeError(w, http.StatusBadGateway, "TTS synthesis failed")
			} else {
				log.Printf("tts: stream step %s (mid-stream): %v", req.StepKey, err)
			}
		}
		return
	}

	// Fallback: blocking synthesis returning a self-contained WAV.
	wav, err := client.Synthesize(ctx, text, voice, style)
	if err != nil {
		log.Printf("tts: synthesize step %s: %v", req.StepKey, err)
		writeError(w, http.StatusBadGateway, "TTS synthesis failed")
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-TTS-Voice", voice)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(wav)
}

// ── POST /debates/{debateID}/interject ──────────────────────────────────────

type interjectRequest struct {
	Text string `json:"text"`
}

type interjectResponse struct {
	Interjection debate.Interjection `json:"interjection"`
}

func (s *Server) interject(w http.ResponseWriter, r *http.Request) {
	debateID := chi.URLParam(r, "debateID")

	defer r.Body.Close()
	var req interjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	it, err := s.svc.Interject(r.Context(), debateID, req.Text)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, interjectResponse{Interjection: it})
}

// ── POST /suggest-topic ─────────────────────────────────────────────────────

type suggestTopicRequest struct {
	DebaterA string `json:"debater_a"`
	DebaterB string `json:"debater_b"`
}

type suggestTopicResponse struct {
	Topics []string `json:"topics"`
}

func (s *Server) suggestTopic(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req suggestTopicRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

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

	ctx := r.Context()
	m, err := modelcache.Get(ctx, config.DefaultModel())
	if err != nil {
		log.Printf("suggest-topic: get model error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get model")
		return
	}

	instruction := fmt.Sprintf(`You propose debate topics for a philosophical debate stage.

**Debater A**: %s — %s
%s

**Debater B**: %s — %s
%s

Propose **5 distinct** debate topics (each a question, 6–14 words) that would produce the most intellectually productive clashes between these two thinkers. The topics should:
- Sit at genuine points of disagreement between their traditions.
- Be accessible to a broad audience — no insider jargon.
- Be phrased as neutral questions, not loaded toward either side.
- Cover different axes of disagreement (ethics, politics, metaphysics, human nature, etc.) — do not repeat the same theme.
- Avoid obvious clichés for this pairing.

Respond with **only the 5 topics**, one per line, no numbering, no bullets, no preamble, no quotation marks, no explanation.`,
		a.Name, a.School, a.Identity,
		b.Name, b.School, b.Identity,
	)

	topicAgent, err := llmagent.New(llmagent.Config{
		Name:        "TopicSuggester",
		Description: "Suggests a debate topic for a pair of philosophers.",
		Model:       m,
		Instruction: instruction,
	})
	if err != nil {
		log.Printf("suggest-topic: create agent error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create agent")
		return
	}

	svc := session.InMemoryService()
	createResp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "logos",
		UserID:  "web",
	})
	if err != nil {
		log.Printf("suggest-topic: create session error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	rn, err := runner.New(runner.Config{
		Agent:          topicAgent,
		AppName:        "logos",
		SessionService: svc,
	})
	if err != nil {
		log.Printf("suggest-topic: runner create error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create runner")
		return
	}

	msg := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{genai.NewPartFromText("suggest a topic")},
	}

	var raw string
	for event, runErr := range rn.Run(ctx, "web", createResp.Session.ID(), msg, agent.RunConfig{}) {
		if runErr != nil {
			log.Printf("suggest-topic: runner error: %v", runErr)
			writeError(w, http.StatusInternalServerError, "runner error")
			return
		}
		if event.Content != nil {
			for _, part := range event.Content.Parts {
				if part.Text != "" {
					raw = part.Text
				}
			}
		}
	}

	topics := parseTopicList(raw)
	if len(topics) == 0 {
		writeError(w, http.StatusInternalServerError, "model returned no topics")
		return
	}

	writeJSON(w, http.StatusOK, suggestTopicResponse{Topics: topics})
}

// parseTopicList splits the model output into individual topics, stripping
// leading list markers (1., 1), -, *, •) and surrounding quotes.
func parseTopicList(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, 5)
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		// strip common list markers: "1.", "1)", "-", "*", "•"
		for {
			trimmed := strings.TrimLeft(t, "0123456789")
			if len(trimmed) < len(t) && len(trimmed) > 0 && (trimmed[0] == '.' || trimmed[0] == ')') {
				t = strings.TrimSpace(trimmed[1:])
				continue
			}
			stripped := false
			for _, prefix := range []string{"-", "*", "•"} {
				if strings.HasPrefix(t, prefix) {
					t = strings.TrimSpace(t[len(prefix):])
					stripped = true
					break
				}
			}
			if stripped {
				continue
			}
			break
		}
		t = strings.Trim(t, "\"'“”‘’")
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
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
