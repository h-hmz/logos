package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// SessionIDFromContext returns the debate id attached to ctx by
// sessionIDMiddleware, or "" when the request is not under the
// /debates/{debateID}/* sub-tree.
func SessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeySessionID).(string)
	return v
}

// sessionIDMiddleware reads chi's bound debateID URL param and stores it on
// the request context so the slog handler can stamp session_id on every log
// line emitted while serving the request. Mount it on the chi sub-router for
// /debates/{debateID}/* routes; mounting at the root would observe an empty
// param and silently drop the field.
func sessionIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "debateID")
		if id != "" {
			ctx := context.WithValue(r.Context(), ctxKeySessionID, id)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
