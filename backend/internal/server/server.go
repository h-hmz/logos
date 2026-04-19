package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
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

var roundStepKeyRe = regexp.MustCompile(`_r\d+$`)

const (
	interjectionMaxLen = 800
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

// formatInterjection wraps user text into the session-state block the next
// philosopher turn reads via {pending_interjection}. Empty text → empty string.
func formatInterjection(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	return fmt.Sprintf(
		"## Audience Challenge\nA member of the audience has raised the following point:\n\n> %s\n\nAddress this challenge directly and specifically before continuing your argument.",
		strings.ReplaceAll(t, "\n", "\n> "),
	)
}

// Server holds the shared state for the HTTP handlers. Concurrency across
// container instances is enforced by the repository's optimistic concurrency
// control (Persisted.Version); this struct carries no per-debate state.
type Server struct {
	philosophers []philosopher.Data
	philMap      map[string]philosopher.Data // name → data
	models       []config.ModelEntry
	repo         debate.Repository
}

// New creates a chi router with all endpoints registered.
func New(philosophers []philosopher.Data, models []config.ModelEntry, repo debate.Repository) *chi.Mux {
	s := &Server{
		philosophers: philosophers,
		philMap:      make(map[string]philosopher.Data, len(philosophers)),
		models:       models,
		repo:         repo,
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

	stepsMeta := debate.BuildStepMeta(a, b, req.NumRounds)

	p := &debate.Persisted{
		ID:           uuid.NewString(),
		Topic:        req.Topic,
		DebaterAName: a.Name,
		DebaterBName: b.Name,
		NumRounds:    req.NumRounds,
		ModelID:      req.Model,
		MaxWords:     req.MaxWords,
		StepOutputs:  map[string]string{},
	}
	if err := s.repo.Create(r.Context(), p); err != nil {
		log.Printf("repo create: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create debate")
		return
	}

	resp := createDebateResponse{
		ID:         p.ID,
		Topic:      p.Topic,
		DebaterA:   debaterInfo{Name: a.Name, School: a.School},
		DebaterB:   debaterInfo{Name: b.Name, School: b.School},
		TotalSteps: len(stepsMeta),
	}
	for _, sm := range stepsMeta {
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
	ctx := r.Context()

	p, err := s.repo.Get(ctx, debateID)
	if errors.Is(err, debate.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Debate not found")
		return
	}
	if err != nil {
		log.Printf("repo get: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to load debate")
		return
	}

	if p.Current >= debate.TotalSteps(p.NumRounds) {
		writeError(w, http.StatusBadRequest, "Debate is already complete")
		return
	}

	rt, err := debate.Hydrate(ctx, p, s.philMap)
	if err != nil {
		log.Printf("hydrate: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to hydrate debate")
		return
	}

	step := rt.Steps[rt.Current]

	rn, err := runner.New(runner.Config{
		Agent:          step.Agent,
		AppName:        "logos",
		SessionService: rt.SessionService,
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

	runCfg := agent.RunConfig{}
	if sse {
		runCfg.StreamingMode = agent.StreamingModeSSE
	}

	var content string
	var turnInput, turnOutput int
	for event, err := range rn.Run(ctx, "web", rt.SessionID, msg, runCfg) {
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

	// Mutate the persisted snapshot.
	if p.StepOutputs == nil {
		p.StepOutputs = map[string]string{}
	}
	p.StepOutputs[step.Key] = content
	p.TotalInputTokens += turnInput
	p.TotalOutputTokens += turnOutput
	stepIndex := p.Current
	p.Current++

	// If an interjection was pending for this step, mark it consumed and drop
	// the staged text so the next Hydrate doesn't re-seed it.
	for i := range p.Interjections {
		it := &p.Interjections[i]
		if it.BeforeStepIndex == stepIndex && it.ConsumedByKey == "" {
			it.ConsumedByKey = step.Key
			p.PendingInterjection = ""
			break
		}
	}

	if err := s.repo.Update(ctx, p); err != nil {
		// Concurrent write (another /next or /interject won the race). The
		// generated turn used a stale view of the debate, so we discard it
		// and let the client refetch state and retry.
		if errors.Is(err, debate.ErrVersionConflict) {
			const msg = "debate state changed concurrently; refresh and retry"
			if sse {
				sw.SendError(msg)
			} else {
				writeError(w, http.StatusConflict, msg)
			}
			return
		}
		log.Printf("repo update: %v", err)
		if sse {
			sw.SendError("failed to save debate state")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to save debate state")
		}
		return
	}

	role := roleForStep(step.Key, rt.DebaterA, rt.DebaterB)

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
		TotalSteps:        debate.TotalSteps(p.NumRounds),
		Done:              p.Current >= debate.TotalSteps(p.NumRounds),
		TurnInputTokens:   turnInput,
		TurnOutputTokens:  turnOutput,
		TotalInputTokens:  p.TotalInputTokens,
		TotalOutputTokens: p.TotalOutputTokens,
	}

	if sse {
		sw.SendDone(result)
	} else {
		writeJSON(w, http.StatusOK, result)
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

	role := roleForStep(req.StepKey, a, b)
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
	ctx := r.Context()

	defer r.Body.Close()
	var req interjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "interjection text is empty")
		return
	}
	if len(text) > interjectionMaxLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("interjection exceeds %d characters", interjectionMaxLen))
		return
	}

	// Retry on version conflict: the read-modify-write here is cheap and
	// pure, so we transparently re-run it if another writer advanced the
	// debate between Get and Update.
	const maxAttempts = 5
	var it debate.Interjection
	for attempt := 0; attempt < maxAttempts; attempt++ {
		p, err := s.repo.Get(ctx, debateID)
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

		stepsMeta := debate.BuildStepMeta(a, b, p.NumRounds)
		if p.Current >= len(stepsMeta) {
			writeError(w, http.StatusBadRequest, "Debate is already complete")
			return
		}
		if p.Current == 0 {
			writeError(w, http.StatusBadRequest, "Cannot interject before the moderator has spoken")
			return
		}
		next := stepsMeta[p.Current]
		role := roleForStep(next.Key, a, b)
		if (role != "a" && role != "b") || !roundStepKeyRe.MatchString(next.Key) {
			writeError(w, http.StatusBadRequest, "Interjections are only allowed before a round turn")
			return
		}
		for _, existing := range p.Interjections {
			if existing.BeforeStepIndex == p.Current && existing.ConsumedByKey == "" {
				writeError(w, http.StatusBadRequest, "An interjection is already pending for this gap")
				return
			}
		}

		p.PendingInterjection = formatInterjection(text)
		it = debate.Interjection{
			Text:            text,
			BeforeStepIndex: p.Current,
		}
		p.Interjections = append(p.Interjections, it)

		err = s.repo.Update(ctx, p)
		if err == nil {
			writeJSON(w, http.StatusOK, interjectResponse{Interjection: it})
			return
		}
		if errors.Is(err, debate.ErrVersionConflict) {
			continue
		}
		log.Printf("repo update: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to stage interjection")
		return
	}
	writeError(w, http.StatusConflict, "debate state changed concurrently; please retry")
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

