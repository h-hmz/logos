// Package auth verifies caller identity for protected routes.
//
// The Verifier interface lets handlers and middleware depend on an abstract
// "ID token → UID" operation so tests can inject a fake without pulling in the
// Firebase Admin SDK. Production uses the Firebase verifier, which validates
// ID tokens against Google's rotating public keys. Local dev and tests use the
// noop verifier, which returns a fixed UID without a network call.
package auth

import (
	"context"
	"errors"

	firebase "firebase.google.com/go/v4"
	firebaseauth "firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

// ErrInvalidToken is returned when a token is missing, malformed, or rejected
// by the upstream verifier. Middleware maps this to 401.
var ErrInvalidToken = errors.New("invalid id token")

// Identity is the verified caller surfaced by a Verifier: the stable UID plus
// the email claim used for allowlist authorization. EmailVerified mirrors the
// token's email_verified claim; Allowlist matching requires it to be true so a
// caller can't claim a whitelisted domain with an unverified address.
type Identity struct {
	UID           string
	Email         string
	EmailVerified bool
}

// Verifier validates a bearer ID token and returns the caller's Identity.
type Verifier interface {
	Verify(ctx context.Context, idToken string) (Identity, error)
}

// firebaseVerifier wraps the Firebase Admin SDK's auth client.
type firebaseVerifier struct {
	client *firebaseauth.Client
}

// NewFirebaseVerifier constructs a Verifier backed by Firebase Auth. The first
// VerifyIDToken call lazily fetches Google's signing keys (~100ms); keys are
// cached ~6h in-process, so subsequent verifies are local RSA.
func NewFirebaseVerifier(ctx context.Context, projectID string, opts ...option.ClientOption) (Verifier, error) {
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID}, opts...)
	if err != nil {
		return nil, err
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, err
	}
	return &firebaseVerifier{client: client}, nil
}

func (v *firebaseVerifier) Verify(ctx context.Context, idToken string) (Identity, error) {
	tok, err := v.client.VerifyIDToken(ctx, idToken)
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	id := Identity{UID: tok.UID}
	// email / email_verified are standard claims on Google/Firebase ID tokens,
	// but Claims is map[string]interface{}, so type-assert defensively.
	if email, ok := tok.Claims["email"].(string); ok {
		id.Email = email
	}
	if verified, ok := tok.Claims["email_verified"].(bool); ok {
		id.EmailVerified = verified
	}
	return id, nil
}

// noopVerifier accepts any non-empty token and returns a fixed dev identity.
// Intended for local development and tests; never wire this in production. The
// dev identity carries a verified email so it can be admitted by an allowlist
// (e.g. AUTH_ALLOWLIST=localhost or =*).
type noopVerifier struct{ id Identity }

// NewNoopVerifier returns a Verifier that yields a stable dev identity. An
// empty token still fails so middleware's "missing Authorization" path stays
// exercised in tests.
func NewNoopVerifier() Verifier {
	return &noopVerifier{id: Identity{UID: "dev-local", Email: "dev@localhost", EmailVerified: true}}
}

func (v *noopVerifier) Verify(_ context.Context, idToken string) (Identity, error) {
	if idToken == "" {
		return Identity{}, ErrInvalidToken
	}
	return v.id, nil
}
