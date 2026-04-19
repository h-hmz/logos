package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
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
