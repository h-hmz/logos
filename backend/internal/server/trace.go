package server

import (
	"context"
	"encoding/hex"
	"net/http"
	"strings"
)

const traceparentHeader = "traceparent"

// TraceIDFromContext returns the 32-hex W3C trace id parsed from the
// traceparent request header, or "" when the header was absent or malformed.
func TraceIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyTraceID).(string)
	return v
}

// SpanIDFromContext returns the 16-hex span id parsed from the traceparent
// request header, or "" when not present.
func SpanIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeySpanID).(string)
	return v
}

// traceContextMiddleware parses the W3C traceparent header that Cloud Run
// automatically populates for incoming requests. Trace id and span id are
// stored on context; when the header is absent or malformed, the context
// values are left empty and request_id remains the correlation id of last
// resort.
func traceContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if traceID, spanID, ok := parseTraceparent(r.Header.Get(traceparentHeader)); ok {
			ctx = context.WithValue(ctx, ctxKeyTraceID, traceID)
			ctx = context.WithValue(ctx, ctxKeySpanID, spanID)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// parseTraceparent parses a W3C traceparent header value of the form
// "VERSION-TRACE_ID-SPAN_ID-FLAGS", e.g.
// "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01". VERSION must be
// 2 hex chars and not "ff"; TRACE_ID must be 32 hex chars and not all zeros;
// SPAN_ID must be 16 hex chars and not all zeros; FLAGS must be 2 hex chars.
// Extra trailing fields are tolerated per the W3C forward-compat rule.
func parseTraceparent(h string) (traceID, spanID string, ok bool) {
	parts := strings.Split(h, "-")
	if len(parts) < 4 {
		return "", "", false
	}
	version, tid, sid, flags := parts[0], parts[1], parts[2], parts[3]
	if !isHex(version, 2) || version == "ff" {
		return "", "", false
	}
	if !isHex(tid, 32) || isAllZero(tid) {
		return "", "", false
	}
	if !isHex(sid, 16) || isAllZero(sid) {
		return "", "", false
	}
	if !isHex(flags, 2) {
		return "", "", false
	}
	return tid, sid, true
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func isAllZero(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '0' {
			return false
		}
	}
	return true
}
