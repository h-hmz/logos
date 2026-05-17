package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/h-hmz/logos/internal/auth"
)

// authMiddleware gates protected routes behind a verified Firebase ID
// token. Runs inside the public chi.Mux so requestIDMiddleware has already
// tagged the context; log records for 401s still carry request_id.
func authMiddleware(v auth.Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(h, prefix) {
				writeError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			token := strings.TrimSpace(h[len(prefix):])
			if token == "" {
				writeError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			uid, err := v.Verify(r.Context(), token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyUID, uid)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
