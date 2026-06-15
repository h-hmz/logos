package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	p, err := s.svc.Get(r.Context(), debateID)
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
	key := tts.NarrationKey(debateID, text, voice, style, config.TTSModel())

	// Serve a persisted narration if one exists. Fail-open: a store read error
	// is logged and falls through to synthesis (ADR 0012).
	if rc, ok, err := s.narrations.Open(ctx, key); err != nil {
		slog.WarnContext(ctx, "tts: narration store read failed; synthesizing", "key", key, "error", err)
	} else if ok {
		defer rc.Close()
		w.Header().Set("Content-Type", fmt.Sprintf("audio/L16;rate=%d;channels=1", tts.SampleRate))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-TTS-Voice", voice)
		w.Header().Set("X-TTS-Cache", "hit")
		w.WriteHeader(http.StatusOK)
		if _, err := io.Copy(w, rc); err != nil {
			slog.ErrorContext(ctx, "tts: cached narration stream interrupted", "key", key, "error", err)
		}
		return
	}

	// Miss: synthesize, teeing the PCM into a buffer so a complete clip can be
	// persisted on success. The tee preserves http.Flusher for progressive
	// playback — a plain io.MultiWriter would erase it.
	var buf bytes.Buffer
	tee := &teeFlushWriter{w: w, buf: &buf}
	headerSent := false
	onStart := func(rate int) {
		w.Header().Set("Content-Type", fmt.Sprintf("audio/L16;rate=%d;channels=1", rate))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-TTS-Voice", voice)
		w.Header().Set("X-TTS-Cache", "miss")
		w.WriteHeader(http.StatusOK)
		headerSent = true
	}
	if err := tts.SynthesizeStream(ctx, text, voice, style, onStart, tee); err != nil {
		if !headerSent {
			slog.ErrorContext(ctx, "tts stream step failed", "step_key", req.StepKey, "error", err)
			writeError(w, http.StatusBadGateway, "TTS synthesis failed")
		} else {
			slog.ErrorContext(ctx, "tts stream step interrupted mid-stream", "step_key", req.StepKey, "error", err)
		}
		return
	}

	// Persist best-effort: the client already has the audio, so a store failure
	// must not affect the response (ADR 0012).
	if err := s.narrations.Put(ctx, key, buf.Bytes()); err != nil {
		slog.WarnContext(ctx, "tts: failed to persist narration", "key", key, "error", err)
	}
}

// teeFlushWriter writes streamed PCM to the client and mirrors it into buf for
// persistence, while preserving the http.Flusher that tts.SynthesizeStream uses
// for progressive playback — a plain io.MultiWriter would erase the Flusher and
// silently buffer the response.
type teeFlushWriter struct {
	w   http.ResponseWriter
	buf *bytes.Buffer
}

func (t *teeFlushWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		t.buf.Write(p[:n])
	}
	return n, err
}

func (t *teeFlushWriter) Flush() {
	if f, ok := t.w.(http.Flusher); ok {
		f.Flush()
	}
}
