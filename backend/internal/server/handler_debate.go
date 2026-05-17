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

// ── POST /debates ───────────────────────────────────────────────────────────

type createDebateRequest struct {
	Topic     string `json:"topic"`
	DebaterA  string `json:"debater_a"`
	DebaterB  string `json:"debater_b"`
	NumRounds int    `json:"num_rounds"`
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
		DebaterA:   debaterInfo{Name: res.DebaterA.Name, School: res.DebaterA.School},
		DebaterB:   debaterInfo{Name: res.DebaterB.Name, School: res.DebaterB.School},
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
}

func (s *Server) nextTurn(w http.ResponseWriter, r *http.Request) {
	debateID := chi.URLParam(r, "debateID")
	defer r.Body.Close()

	res, err := s.svc.NextTurn(r.Context(), debateID)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}

	writeJSON(w, http.StatusOK, turnResult{
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
	})
}

