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
	"sync"
	"time"

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

// synthesizeStep turns the given text into speech using the voice and style for
// the named step, resolved from the step key against the philosopher catalog —
// no debate load, so a cache hit is a pure blob fetch (ADR 0012). The text is
// taken from the request so the client can replay arbitrary content it already
// has without the server storing turn outputs.
func (s *Server) synthesizeStep(w http.ResponseWriter, r *http.Request) {
	if !config.TTSEnabled() {
		writeError(w, http.StatusServiceUnavailable, "TTS is disabled on this server")
		return
	}

	start := time.Now()
	debateID := chi.URLParam(r, "debateID")

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

	voice, style, ok := tts.VoiceForStepKey(req.StepKey, s.philosophers)
	if !ok {
		writeError(w, http.StatusBadRequest, "step is not speakable")
		return
	}

	ctx := r.Context()
	uid := UIDFromContext(ctx)
	pricing := tts.Pricing{
		USDPer1MAudioTokens: config.USDPer1MAudioTokens(),
		USDPerGBEgress:      config.USDPerGBEgress(),
	}

	// Reserve the estimated cost up front so concurrent narrations cannot
	// collectively overshoot the budget; reconcile to the exact byte count after
	// streaming (ADR 0013). Over budget → 402 before any bytes flow.
	reserved, err := s.svc.ReserveNarration(ctx, uid, pricing.EstimateUSD(len(text)))
	if errors.Is(err, debate.ErrOverBudget) {
		writeError(w, http.StatusPaymentRequired, "Your purse is empty.")
		return
	}
	var deliveredBytes int
	var synthesized bool
	defer func() {
		actual := pricing.ActualUSD(deliveredBytes, synthesized)
		s.svc.SettleNarration(ctx, uid, reserved, actual)
		cache := "hit"
		if synthesized {
			cache = "miss"
		}
		slog.LogAttrs(ctx, slog.LevelInfo, "tts_usage",
			slog.String("event", "tts_usage"),
			slog.String("cache", cache),
			slog.String("debate_id", debateID),
			slog.String("step_key", req.StepKey),
			slog.String("uid", uid),
			slog.Int("bytes", deliveredBytes),
			slog.Float64("cost_usd", actual),
			slog.Float64("reserved_usd", reserved),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
	}()

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
		n, err := io.Copy(w, rc)
		deliveredBytes = int(n) // a hit is egress only (synthesized stays false)
		if err != nil {
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
	serr := tts.SynthesizeStream(ctx, text, voice, style, onStart, tee)
	// Charge for the bytes actually synthesized and delivered, even on a
	// mid-stream failure — the provider already billed them (ADR 0013).
	deliveredBytes, synthesized = buf.Len(), true
	if serr != nil {
		if !headerSent {
			slog.ErrorContext(ctx, "tts stream step failed", "step_key", req.StepKey, "error", serr)
			writeError(w, http.StatusBadGateway, "TTS synthesis failed")
		} else {
			slog.ErrorContext(ctx, "tts stream step interrupted mid-stream", "step_key", req.StepKey, "error", serr)
		}
		return
	}

	// Persist best-effort: the client already has the audio, so a store failure
	// must not affect the response (ADR 0012).
	if err := s.narrations.Put(ctx, key, buf.Bytes()); err != nil {
		slog.WarnContext(ctx, "tts: failed to persist narration", "key", key, "error", err)
	}
}

// ── GET /debates/{debateID}/narrations ───────────────────────────────────────

type narrationsStatusResponse struct {
	// Cached maps a narratable step key to whether its narration already exists
	// in the store. A true value means playback is an instant replay (egress
	// only); false means playback will synthesize and incur the synthesis charge.
	Cached map[string]bool `json:"cached"`
}

// narrationsStatus reports, per narratable step, whether a narration already
// exists — so the client can warn before a play that will synthesize (a cache
// miss) versus an instant replay (a hit).
func (s *Server) narrationsStatus(w http.ResponseWriter, r *http.Request) {
	if !config.TTSEnabled() {
		writeError(w, http.StatusServiceUnavailable, "TTS is disabled on this server")
		return
	}

	debateID := chi.URLParam(r, "debateID")
	res, err := s.svc.GetSession(r.Context(), debateID)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}

	ctx := r.Context()
	cached := make(map[string]bool, len(res.Turns))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range res.Turns {
		voice, style, ok := tts.VoiceForStepKey(t.Step.Key, s.philosophers)
		text := stripParentheticals(t.Content)
		if !ok || text == "" {
			continue // not a narratable step
		}
		// Key over the stripped text and the TTS model, exactly as synthesizeStep
		// does, so this presence check matches the key a real play would store under.
		key := tts.NarrationKey(debateID, text, voice, style, config.TTSModel())
		stepKey := t.Step.Key
		wg.Add(1)
		go func() {
			defer wg.Done()
			exists, eerr := s.narrations.Exists(ctx, key)
			if eerr != nil {
				slog.WarnContext(ctx, "tts: narration exists check failed; reporting not cached", "key", key, "error", eerr)
				exists = false
			}
			mu.Lock()
			cached[stepKey] = exists
			mu.Unlock()
		}()
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, narrationsStatusResponse{Cached: cached})
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
