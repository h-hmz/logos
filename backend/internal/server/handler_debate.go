package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/h-hmz/logos/internal/debate"
)

// mapServiceError maps a domain error to an HTTP status + user-facing message.
// Internal errors return 500 with a generic message; callers should log the
// underlying err separately.
func mapServiceError(err error) (int, string) {
	var ve *debate.ValidationError
	switch {
	case errors.Is(err, debate.ErrNotFound):
		return http.StatusNotFound, "Debate not found"
	case errors.Is(err, debate.ErrVersionConflict):
		return http.StatusConflict, "debate state changed concurrently; refresh and retry"
	case errors.Is(err, debate.ErrGenerationInFlight):
		return http.StatusConflict, "a generation is already in progress; wait for it to finish"
	case errors.Is(err, debate.ErrForbidden):
		return http.StatusForbidden, "only the debate's owner can advance it"
	case errors.Is(err, debate.ErrOverBudget):
		return http.StatusPaymentRequired, "generation quota exhausted"
	case errors.As(err, &ve):
		return http.StatusBadRequest, ve.Msg
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

// writeServiceError maps domain errors to HTTP responses.
func writeServiceError(ctx context.Context, w http.ResponseWriter, err error) {
	status, msg := mapServiceError(err)
	if status == http.StatusInternalServerError {
		slog.ErrorContext(ctx, "service error", "error", err)
	}
	writeError(w, status, msg)
}

// ── POST /debates ───────────────────────────────────────────────────────────

type createDebateRequest struct {
	Topic     string `json:"topic"`
	DebaterA  string `json:"debater_a"`
	DebaterB  string `json:"debater_b"`
	NumRounds int    `json:"num_rounds"`
	MaxWords  int    `json:"max_words"`
	// ModelA/ModelB are the optional per-debater models. Empty falls back to the
	// default model; when set they must be in the authorized allowlist.
	ModelA string `json:"model_a"`
	ModelB string `json:"model_b"`
}

type stepInfo struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type debaterInfo struct {
	Name   string `json:"name"`
	School string `json:"school"`
	// Model is the resolved model this debater runs on (the Default Model when
	// the caller didn't pick one), surfaced so the UI can label each corner.
	Model string `json:"model"`
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
	uid := UIDFromContext(r.Context())
	defer r.Body.Close()
	var req createDebateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	res, err := s.svc.CreateDebate(r.Context(), uid, debate.CreateDebateParams{
		Topic:     req.Topic,
		DebaterA:  req.DebaterA,
		DebaterB:  req.DebaterB,
		NumRounds: req.NumRounds,
		MaxWords:  req.MaxWords,
		ModelA:    req.ModelA,
		ModelB:    req.ModelB,
	})
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}

	resp := createDebateResponse{
		ID:         res.Debate.ID,
		Topic:      res.Debate.Topic,
		DebaterA:   debaterInfo{Name: res.DebaterA.Name, School: res.DebaterA.School, Model: res.Debate.Models.DebaterA},
		DebaterB:   debaterInfo{Name: res.DebaterB.Name, School: res.DebaterB.School, Model: res.Debate.Models.DebaterB},
		TotalSteps: len(res.Steps),
	}
	for _, sm := range res.Steps {
		resp.Steps = append(resp.Steps, stepInfo{Key: sm.Key, Label: sm.Label})
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── GET /debates/{debateID} ─────────────────────────────────────────────────

type getDebateResponse struct {
	ID         string             `json:"id"`
	Topic      string             `json:"topic"`
	DebaterA   debaterInfo        `json:"debater_a"`
	DebaterB   debaterInfo        `json:"debater_b"`
	Steps      []stepInfo         `json:"steps"`
	TotalSteps int                `json:"total_steps"`
	Turns      map[int]turnResult `json:"turns"`
}

func (s *Server) getDebate(w http.ResponseWriter, r *http.Request) {
	debateID := chi.URLParam(r, "debateID")
	res, err := s.svc.GetSession(r.Context(), debateID)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	resp := getDebateResponse{
		ID:         res.Debate.ID,
		Topic:      res.Debate.Topic,
		DebaterA:   debaterInfo{Name: res.DebaterA.Name, School: res.DebaterA.School, Model: res.Debate.Models.DebaterA},
		DebaterB:   debaterInfo{Name: res.DebaterB.Name, School: res.DebaterB.School, Model: res.Debate.Models.DebaterB},
		TotalSteps: len(res.Steps),
		Turns:      make(map[int]turnResult, len(res.Turns)),
	}
	for _, m := range res.Steps {
		resp.Steps = append(resp.Steps, stepInfo{Key: m.Key, Label: m.Label})
	}
	for i, t := range res.Turns {
		resp.Turns[i] = turnResult{
			Key:               t.Step.Key,
			Label:             t.Step.Label,
			Author:            t.AgentName,
			Role:              t.Role,
			Content:           t.Content,
			StepIndex:         t.StepIndex,
			TotalSteps:        t.TotalSteps,
			Done:              t.Done,
			TotalInputTokens:  t.TotalInputTokens,
			TotalOutputTokens: t.TotalOutputTokens,
			Interjection:      t.Interjection,
		}
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
	StepIndex         int    `json:"step_index"`
	TotalSteps        int    `json:"total_steps"`
	Done              bool   `json:"done"`
	TurnInputTokens   int    `json:"turn_input_tokens"`
	TurnOutputTokens  int    `json:"turn_output_tokens"`
	TotalInputTokens  int    `json:"total_input_tokens"`
	TotalOutputTokens int    `json:"total_output_tokens"`
	Interjection      string `json:"interjection,omitempty"`
}

// nextTurnRequest is the optional body of POST /next; an absent/empty body is a
// plain advance. Interjection length/role validation happens in the service.
type nextTurnRequest struct {
	Interjection string `json:"interjection"`
}

// decodeInterjection reads the optional interjection from the request body. An
// empty body is valid (a plain advance), so io.EOF is not an error; bad JSON is.
func decodeInterjection(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}
	var req nextTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	return req.Interjection, nil
}

func (s *Server) nextTurn(w http.ResponseWriter, r *http.Request) {
	debateID := chi.URLParam(r, "debateID")
	uid := UIDFromContext(r.Context())
	defer r.Body.Close()

	interjection, err := decodeInterjection(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if wantsSSE(r) {
		s.nextTurnSSE(w, r, uid, debateID, interjection)
		return
	}

	res, err := s.svc.NextTurn(r.Context(), uid, debateID, interjection, nil)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}

	writeJSON(w, http.StatusOK, toTurnResultPayload(res))
}

// nextTurnSSE runs the next turn and streams text deltas to the client as SSE
// "token" events, terminating with a "done" event carrying the full payload
// or an "error" event on failure. The quota gate (lock + budget) runs before
// any token is streamed, so its rejections (402/409) and other pre-stream
// failures surface as real HTTP statuses. Once an event has been flushed
// (status 200 committed), failures can only ride the SSE error event.
func (s *Server) nextTurnSSE(w http.ResponseWriter, r *http.Request, uid, debateID, interjection string) {
	sw, ok := newSSEWriter(r.Context(), w)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	res, err := s.svc.NextTurn(r.Context(), uid, debateID, interjection, sw.SendToken)
	if err != nil {
		status, msg := mapServiceError(err)
		if status == http.StatusInternalServerError {
			slog.ErrorContext(r.Context(), "service error (sse)", "error", err)
		}
		// If nothing has been streamed yet, the HTTP status is still open —
		// return a real status (e.g. 402/409 from the quota gate) instead of
		// burying the failure in an SSE error event the client must parse.
		if !sw.committed() {
			writeError(w, status, msg)
			return
		}
		sw.SendError(msg)
		return
	}
	sw.SendDone(toTurnResultPayload(res))
}

func toTurnResultPayload(res *debate.TurnResult) turnResult {
	return turnResult{
		Key:               res.Step.Key,
		Label:             res.Step.Label,
		Author:            res.AgentName,
		Role:              res.Role,
		Content:           res.Content,
		StepIndex:         res.StepIndex,
		TotalSteps:        res.TotalSteps,
		Done:              res.Done,
		TurnInputTokens:   res.TurnInputTokens,
		TurnOutputTokens:  res.TurnOutputTokens,
		TotalInputTokens:  res.TotalInputTokens,
		TotalOutputTokens: res.TotalOutputTokens,
		Interjection:      res.Interjection,
	}
}

