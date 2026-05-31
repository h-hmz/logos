package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/h-hmz/logos/internal/auth"
)

// authMiddleware gates protected routes behind a verified Firebase ID token
// and an email allowlist. A missing/invalid token is 401; a valid token whose
// email is not on the allowlist is 403. Runs inside the public chi.Mux so
// requestIDMiddleware has already tagged the context; log records still carry
// request_id.
func authMiddleware(v auth.Verifier, allow auth.Allowlist) func(http.Handler) http.Handler {
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
			id, err := v.Verify(r.Context(), token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			if !allow.Allows(id) {
				slog.WarnContext(r.Context(), "auth: caller not in allowlist",
					"uid", id.UID, "email", id.Email, "email_verified", id.EmailVerified)
				writeError(w, http.StatusForbidden, "access denied")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyUID, id.UID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
