package server

import (
	"log/slog"
	"net/http"
)

// quotaUnlimited is the sentinel limit/remaining value reported when quota
// enforcement is disabled. JSON can't encode +Inf, so a negative value stands
// in for "no ceiling"; clients treat any negative limit as unlimited.
const quotaUnlimited = -1

// quotaResponse reports a caller's lifetime generation budget in raw USD.
// Remaining is clamped at zero so clients never see a negative balance after a
// bounded overshoot (ADR 0009). A negative LimitUSD/RemainingUSD means
// unlimited (quota disabled).
type quotaResponse struct {
	LimitUSD     float64 `json:"limit_usd"`
	SpentUSD     float64 `json:"spent_usd"`
	RemainingUSD float64 `json:"remaining_usd"`
}

// getQuota returns the authenticated caller's quota usage. When quota is
// disabled it reports an unlimited budget so the frontend can render
// consistently without special-casing the feature flag.
func (s *Server) getQuota(w http.ResponseWriter, r *http.Request) {
	uid := UIDFromContext(r.Context())

	usage, err := s.svc.QuotaUsage(r.Context(), uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "quota: read failed for /me/quota", "uid", uid, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !usage.Enabled {
		writeJSON(w, http.StatusOK, quotaResponse{
			LimitUSD:     quotaUnlimited,
			SpentUSD:     0,
			RemainingUSD: quotaUnlimited,
		})
		return
	}

	writeJSON(w, http.StatusOK, quotaResponse{
		LimitUSD:     usage.LimitUSD,
		SpentUSD:     usage.SpentUSD,
		RemainingUSD: usage.Remaining,
	})
}
