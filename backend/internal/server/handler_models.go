package server

import (
	"net/http"

	"github.com/h-hmz/logos/internal/models"
)

// ── GET /models ─────────────────────────────────────────────────────────────

// modelItem is one authorized model in the GET /models response. It is an
// object (not a bare string) so display metadata (e.g. a label) can be added
// later without reshaping the JSON.
type modelItem struct {
	ID string `json:"id"`
}

// listModels returns the authorized Debater Models — the allowlist that backs
// the (future) per-debater model dropdown.
func (s *Server) listModels(w http.ResponseWriter, _ *http.Request) {
	ids := models.List()
	items := make([]modelItem, len(ids))
	for i, id := range ids {
		items[i] = modelItem{ID: id}
	}
	writeJSON(w, http.StatusOK, items)
}
