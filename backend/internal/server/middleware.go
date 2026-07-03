package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
)

const requestIDHeader = "X-Request-Id"

// maxRequestBodyBytes caps the request body size any handler will read. The
// largest legitimate payload (a full tutor request) is well under 256 KiB;
// 1 MiB leaves generous headroom.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// bodyReadTimeout is the maximum time a client may take to deliver its entire
// request body. Bodies are capped at maxRequestBodyBytes, so any legitimate
// upload finishes well within it.
const bodyReadTimeout = 30 * time.Second

// guardBody bounds request bodies in size and time: the body is read up front
// (capped by http.MaxBytesReader) under a connection read deadline, then
// handed to the handler as an in-memory buffer. The deadline is cleared before
// the handler runs, so long-lived streaming responses (SSE, TTS) are
// unaffected — unlike a server-wide ReadTimeout, which would expire
// mid-response. Overflow returns 413, a stalled upload 408; bodiless requests
// pass through untouched.
func guardBody(limit int64, timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil || r.Body == http.NoBody {
				next.ServeHTTP(w, r)
				return
			}
			rc := http.NewResponseController(w)
			// Best-effort: a transport without read-deadline support (e.g.
			// httptest.ResponseRecorder) still gets the size cap.
			_ = rc.SetReadDeadline(time.Now().Add(timeout))
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
			if err != nil {
				// Keep the deadline in place: clearing it would let the server's
				// end-of-request body drain block on the same stalled upload.
				var mbe *http.MaxBytesError
				switch {
				case errors.As(err, &mbe):
					writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
				case os.IsTimeout(err):
					writeError(w, http.StatusRequestTimeout, "timed out reading request body")
				default:
					writeError(w, http.StatusBadRequest, "could not read request body")
				}
				return
			}
			// Body fully consumed: clear the deadline so a streaming response is
			// not cut off, and close the original body (handlers close the buffer).
			_ = rc.SetReadDeadline(time.Time{})
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}
}

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

// Unwrap exposes the underlying ResponseWriter so http.NewResponseController
// can reach the connection's deadline methods through this wrapper.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

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
