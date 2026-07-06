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
	"unicode/utf8"

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

// maxTTSTextLen caps the text a single narration request may synthesize,
// bounding the prompt sent on the single upstream synthesis call. The longest
// narratable turn is a few thousand characters, so 20k leaves generous
// headroom. Audio beyond Gemini's ~2-minute per-call output cap is silently
// truncated and accepted as-is (ADR 0002 rev 2026-07-06).
const maxTTSTextLen = 20000

// maxPlausibleCharsPerSec is the fastest speech rate a genuine full read could
// imply (normal delivery measures ~13 chars/sec). A clip whose text length
// divided by audio duration exceeds this cannot contain the whole text — the
// synthesis ended early — and must not be persisted. Mirrors the frontend's
// MAX_PLAUSIBLE_CPS highlight guard (SpokenText.tsx).
const maxPlausibleCharsPerSec = 20

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
		writeDecodeError(w, err)
		return
	}
	if len(req.Text) > maxTTSTextLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("text exceeds %d characters", maxTTSTextLen))
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

	// Gemini sometimes ends synthesis silently before reading the whole text —
	// early EOS at a discourse boundary or the per-call output cap, neither
	// visible through /audio/speech (ADR 0002 rev 2026-07-06). A clip whose
	// implied speaking rate is implausibly fast cannot contain its text;
	// persisting it would make the truncation permanent, so skip the store and
	// let a replay re-roll the synthesis.
	secs := float64(buf.Len()) / float64(2*tts.SampleRate)
	if chars := utf8.RuneCountInString(text); secs*maxPlausibleCharsPerSec < float64(chars) {
		slog.WarnContext(ctx, "tts: clip too short for its text; not persisting",
			"step_key", req.StepKey, "chars", chars, "audio_seconds", secs)
		return
	}

	// Persist best-effort: the client already has the audio, so a store failure
	// must not affect the response (ADR 0012).
	if err := s.narrations.Put(ctx, key, buf.Bytes()); err != nil {
		slog.WarnContext(ctx, "tts: failed to persist narration", "key", key, "error", err)
	}
}

// ── GET /debates/{debateID}/narrations ───────────────────────────────────────

// narrationInfo is the per-step narration status: whether a narration exists and,
// when it does, its synthesis cost — the one-time cost of producing the clip
// (ADR 0013 §6), derived on read from the stored byte count and never persisted.
// SynthesisCostUSD is omitted for an uncached step (nothing is stored to measure)
// and for an unmetered deployment (zero rate), so the client shows no figure in
// either case.
type narrationInfo struct {
	Cached           bool    `json:"cached"`
	SynthesisCostUSD float64 `json:"synthesis_cost_usd,omitempty"`
}

type narrationsStatusResponse struct {
	// Narrations maps each narratable step key to its status. A cached step
	// replays instantly (egress only) and carries its synthesis cost; an uncached
	// step will synthesize (and be charged) on play and carries no cost.
	Narrations map[string]narrationInfo `json:"narrations"`
}

// narrationsStatus reports, per narratable step, whether a narration already
// exists and — when it does — its synthesis cost (ADR 0013 §6, derived on read
// from the stored byte count). The client uses presence to warn before a play
// that will synthesize (a miss) versus an instant replay (a hit), and shows the
// cost as what the narration cost to make.
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
	// Only the synthesis rate is needed — egress is a per-play charge, not a
	// property of a stored narration, so it never surfaces here (ADR 0013 §6).
	pricing := tts.Pricing{USDPer1MAudioTokens: config.USDPer1MAudioTokens()}
	narrations := make(map[string]narrationInfo, len(res.Turns))
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
			size, exists, serr := s.narrations.Stat(ctx, key)
			if serr != nil {
				slog.WarnContext(ctx, "tts: narration stat failed; reporting not cached", "key", key, "error", serr)
				exists = false
			}
			info := narrationInfo{Cached: exists}
			if exists {
				// Synthesis cost is a pure function of the stored bytes (ADR 0013 §6):
				// derived here from the size, never persisted next to the LLM metrics.
				info.SynthesisCostUSD = pricing.SynthesisUSD(int(size))
			}
			mu.Lock()
			narrations[stepKey] = info
			mu.Unlock()
		}()
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, narrationsStatusResponse{Narrations: narrations})
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
