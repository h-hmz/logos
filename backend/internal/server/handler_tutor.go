package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/h-hmz/logos/internal/debate"
)

// ── POST /debates/{debateID}/tutor ──────────────────────────────────────────

type tutorMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type tutorRequest struct {
	Selection string         `json:"selection"`
	TurnKey   string         `json:"turn_key"`
	History   []tutorMessage `json:"history"`
}

type tutorResponse struct {
	Content     string   `json:"content"`
	Suggestions []string `json:"suggestions,omitempty"`
}

func (s *Server) tutorExplain(w http.ResponseWriter, r *http.Request) {
	debateID := chi.URLParam(r, "debateID")

	defer r.Body.Close()
	var req tutorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	history := make([]debate.TutorMessage, len(req.History))
	for i, m := range req.History {
		history[i] = debate.TutorMessage{Role: m.Role, Content: m.Content}
	}

	result, err := s.svc.TutorExplain(r.Context(), debateID, debate.TutorRequest{
		Selection: req.Selection,
		TurnKey:   req.TurnKey,
		History:   history,
	})
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}

	writeJSON(w, http.StatusOK, tutorResponse{Content: result.Content, Suggestions: result.Suggestions})
}
