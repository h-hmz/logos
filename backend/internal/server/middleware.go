package server

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/h-hmz/logos/internal/auth"
)

const requestIDHeader = "X-Request-Id"

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyUID
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

// statusRecorder wraps http.ResponseWriter to capture status and bytes written
// for access logging. http.Flusher is preserved so SSE streaming still works.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// accessLogMiddleware logs one line per request once the handler returns.
func accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		slog.LogAttrs(r.Context(), slog.LevelInfo, "http_request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int("bytes", rec.bytes),
			slog.Duration("duration", time.Since(start)),
			slog.String("remote", clientIP(r)),
		)
	})
}

// recoverMiddleware converts panics into 500 responses and logs the stack.
// Placed inside accessLogMiddleware so the recovered status is still recorded.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.ErrorContext(r.Context(), "panic recovered",
					"error", rec,
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
				)
				// Avoid double-writing headers if the handler already committed.
				if sr, ok := w.(*statusRecorder); ok && sr.wroteHeader {
					return
				}
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
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

// clientIP prefers X-Forwarded-For (first hop) when present, otherwise falls
// back to RemoteAddr. Only trust this behind a load balancer you control.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	return r.RemoteAddr
}
