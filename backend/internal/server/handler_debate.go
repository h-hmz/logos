package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/h-hmz/logos/internal/debate"
)

// writeServiceError maps domain errors to HTTP responses.
func writeServiceError(ctx context.Context, w http.ResponseWriter, err error) {
	var ve *debate.ValidationError
	switch {
	case errors.Is(err, debate.ErrNotFound):
		writeError(w, http.StatusNotFound, "Debate not found")
	case errors.Is(err, debate.ErrVersionConflict):
		writeError(w, http.StatusConflict, "debate state changed concurrently; refresh and retry")
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, ve.Msg)
	default:
		slog.ErrorContext(ctx, "service error", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// serviceErrorMessage returns the user-facing message for a service error,
// used when the transport has already committed to SSE and can't emit an HTTP
// status code.
func serviceErrorMessage(ctx context.Context, err error) string {
	var ve *debate.ValidationError
	switch {
	case errors.Is(err, debate.ErrNotFound):
		return "Debate not found"
	case errors.Is(err, debate.ErrVersionConflict):
		return "debate state changed concurrently; refresh and retry"
	case errors.As(err, &ve):
		return ve.Msg
	default:
		slog.ErrorContext(ctx, "service error (sse)", "error", err)
		return "internal error"
	}
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
		writeServiceError(r.Context(), w, err)
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
		sw, sse = newSSEWriter(r.Context(), w)
	}

	var onToken func(string)
	if sse {
		onToken = sw.SendToken
	}

	res, err := s.svc.NextTurn(r.Context(), debateID, onToken)
	if err != nil {
		if sse {
			sw.SendError(serviceErrorMessage(r.Context(), err))
			return
		}
		writeServiceError(r.Context(), w, err)
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
		writeServiceError(r.Context(), w, err)
		return
	}
	writeJSON(w, http.StatusOK, interjectResponse{Interjection: it})
}
