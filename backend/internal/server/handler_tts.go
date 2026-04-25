package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/tts"
)

// Parenthetical asides (often inline glosses like "asabiyya (social solidarity)")
// read awkwardly aloud. Strip them — with any leading whitespace — before TTS.
var parentheticalRE = regexp.MustCompile(`\s*\([^()]*\)`)

func stripParentheticals(s string) string {
	out := parentheticalRE.ReplaceAllString(s, "")
	out = regexp.MustCompile(`[ \t]+`).ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
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
		slog.ErrorContext(r.Context(), "failed to load debate", "error", err)
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
	text := stripParentheticals(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "text is empty")
		return
	}
	if req.StepKey == "" {
		writeError(w, http.StatusBadRequest, "step_key is required")
		return
	}

	role := debate.RoleForStep(req.StepKey, a, b)
	voice, style, ok := tts.VoiceForStep(role, a, b)
	if !ok {
		writeError(w, http.StatusBadRequest, "step is not speakable")
		return
	}

	ctx := r.Context()
	headerSent := false
	onStart := func(rate int) {
		w.Header().Set("Content-Type", fmt.Sprintf("audio/L16;rate=%d;channels=1", rate))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-TTS-Voice", voice)
		w.WriteHeader(http.StatusOK)
		headerSent = true
	}
	if err := tts.SynthesizeStream(ctx, text, voice, style, onStart, w); err != nil {
		if !headerSent {
			slog.ErrorContext(ctx, "tts stream step failed", "step_key", req.StepKey, "error", err)
			writeError(w, http.StatusBadGateway, "TTS synthesis failed")
		} else {
			slog.ErrorContext(ctx, "tts stream step interrupted mid-stream", "step_key", req.StepKey, "error", err)
		}
	}
}
