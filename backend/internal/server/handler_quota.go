package server

import (
	"log/slog"
	"net/http"
)

// quotaResponse reports a caller's lifetime generation budget in raw USD.
// Remaining is clamped at zero so clients never see a negative balance after a
// bounded overshoot (ADR 0009).
type quotaResponse struct {
	LimitUSD     float64 `json:"limit_usd"`
	SpentUSD     float64 `json:"spent_usd"`
	RemainingUSD float64 `json:"remaining_usd"`
}

// getQuota returns the authenticated caller's quota usage.
func (s *Server) getQuota(w http.ResponseWriter, r *http.Request) {
	uid := UIDFromContext(r.Context())

	usage, err := s.svc.QuotaUsage(r.Context(), uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "quota: read failed for /me/quota", "uid", uid, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, quotaResponse{
		LimitUSD:     usage.LimitUSD,
		SpentUSD:     usage.SpentUSD,
		RemainingUSD: usage.Remaining,
	})
}
