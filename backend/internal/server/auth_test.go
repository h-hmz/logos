package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/h-hmz/logos/internal/auth"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/philosopher"
)

// fakeVerifier records the last token it received and yields a canned result.
// Used to assert middleware maps verifier outcomes to the right status codes.
type fakeVerifier struct {
	id    auth.Identity
	err   error
	token string
}

func (f *fakeVerifier) Verify(_ context.Context, idToken string) (auth.Identity, error) {
	f.token = idToken
	return f.id, f.err
}

// authRouter builds a router backed by the given verifier and a minimal
// server state. Handlers won't be reached in 401 tests, so the roster
// just needs to be non-empty for handler_debate validation to survive. The
// allowlist is open so authorization never masks the authentication outcome
// under test.
func authRouter(t *testing.T, v auth.Verifier) http.Handler {
	t.Helper()
	phils := []philosopher.Data{
		{Name: "Socrates", School: "Classical"},
		{Name: "Kant", School: "German Idealism"},
	}
	return New(phils, debate.NewMemoryRepo(), v, auth.ParseAllowlist("*"))
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	h := authRouter(t, auth.NewNoopVerifier())
	req := httptest.NewRequest(http.MethodPost, "/debates", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestAuthMiddleware_MalformedHeader(t *testing.T) {
	h := authRouter(t, auth.NewNoopVerifier())
	req := httptest.NewRequest(http.MethodPost, "/debates", nil)
	req.Header.Set("Authorization", "NotBearer abc")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestAuthMiddleware_EmptyToken(t *testing.T) {
	h := authRouter(t, auth.NewNoopVerifier())
	req := httptest.NewRequest(http.MethodPost, "/debates", nil)
	req.Header.Set("Authorization", "Bearer   ")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestAuthMiddleware_RejectedByVerifier(t *testing.T) {
	fv := &fakeVerifier{err: errors.New("bad")}
	h := authRouter(t, fv)
	req := httptest.NewRequest(http.MethodPost, "/debates", nil)
	req.Header.Set("Authorization", "Bearer junk")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if fv.token != "junk" {
		t.Fatalf("verifier saw token %q, want %q", fv.token, "junk")
	}
}

func TestAuthMiddleware_PublicRoutesOpen(t *testing.T) {
	h := authRouter(t, &fakeVerifier{err: errors.New("should not be called")})
	for _, path := range []string{"/healthz", "/readyz", "/philosophers"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code == http.StatusUnauthorized {
			t.Fatalf("%s should be public, got 401", path)
		}
	}
}

func TestUIDFromContext_SetOnProtected(t *testing.T) {
	fv := &fakeVerifier{id: auth.Identity{UID: "user-xyz", Email: "u@example.com", EmailVerified: true}}
	// Swap in a handler that captures the UID from context after middleware runs.
	var gotUID string
	mw := authMiddleware(fv, auth.ParseAllowlist("example.com"))
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotUID = UIDFromContext(r.Context())
	})
	req := httptest.NewRequest(http.MethodPost, "/debates", nil)
	req.Header.Set("Authorization", "Bearer ok")
	rr := httptest.NewRecorder()
	mw(inner).ServeHTTP(rr, req)
	if gotUID != "user-xyz" {
		t.Fatalf("UIDFromContext = %q, want %q", gotUID, "user-xyz")
	}
}

// TestAuthMiddleware_Allowlist asserts that a verified caller is admitted only
// when their email matches the allowlist, and rejected with 403 otherwise.
func TestAuthMiddleware_Allowlist(t *testing.T) {
	cases := []struct {
		name     string
		spec     string
		id       auth.Identity
		wantCode int
	}{
		{"domain match", "example.com", auth.Identity{Email: "a@example.com", EmailVerified: true}, http.StatusOK},
		{"email match", "a@example.com", auth.Identity{Email: "a@example.com", EmailVerified: true}, http.StatusOK},
		{"case insensitive", "Example.COM", auth.Identity{Email: "A@Example.com", EmailVerified: true}, http.StatusOK},
		{"wrong domain", "example.com", auth.Identity{Email: "a@evil.com", EmailVerified: true}, http.StatusForbidden},
		{"unverified email", "example.com", auth.Identity{Email: "a@example.com", EmailVerified: false}, http.StatusForbidden},
		{"empty allowlist denies", "", auth.Identity{Email: "a@example.com", EmailVerified: true}, http.StatusForbidden},
		{"wildcard allows any", "*", auth.Identity{Email: "a@evil.com", EmailVerified: false}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fv := &fakeVerifier{id: tc.id}
			mw := authMiddleware(fv, auth.ParseAllowlist(tc.spec))
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPost, "/debates", nil)
			req.Header.Set("Authorization", "Bearer ok")
			rr := httptest.NewRecorder()
			mw(inner).ServeHTTP(rr, req)
			if rr.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rr.Code, tc.wantCode)
			}
		})
	}
}
