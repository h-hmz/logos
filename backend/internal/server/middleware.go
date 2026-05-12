package server

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/google/uuid"

	"github.com/h-hmz/logos/internal/auth"
)

const requestIDHeader = "X-Request-Id"

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyUID
	ctxKeyTraceID
	ctxKeySpanID
	ctxKeySessionID
)

// UIDFromContext returns the authenticated UID attached to ctx, or "" if the
// request did not pass through authMiddleware.
func UIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUID).(string)
	return v
}

// RequestIDFromContext returns the request ID attached to ctx, or "" if none.
// The root slog handler reads this to stamp request_id on every log record,
// so callers can keep using slog.*Context without threading a logger around.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// requestIDMiddleware accepts an incoming X-Request-Id, generating one when
// absent, and propagates it via response header and context.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// statusRecorder wraps http.ResponseWriter to track whether the response has
// been committed, so recoverMiddleware can skip writing a 500 over a response
// the handler already started. http.Flusher is preserved so SSE still works.
type statusRecorder struct {
	http.ResponseWriter
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// recoverMiddleware converts panics into 500 responses and logs the stack.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			if rv := recover(); rv != nil {
				slog.ErrorContext(r.Context(), "panic recovered",
					"error", rv,
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
				)
				// Avoid writing a 500 over a response the handler already committed.
				if rec.wroteHeader {
					return
				}
				writeError(rec, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

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
