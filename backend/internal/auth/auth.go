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

// Verifier validates a bearer ID token and returns the caller's stable UID.
type Verifier interface {
	Verify(ctx context.Context, idToken string) (uid string, err error)
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

func (v *firebaseVerifier) Verify(ctx context.Context, idToken string) (string, error) {
	tok, err := v.client.VerifyIDToken(ctx, idToken)
	if err != nil {
		return "", ErrInvalidToken
	}
	return tok.UID, nil
}

// noopVerifier accepts any non-empty token and returns a fixed UID. Intended
// for local development and tests; never wire this in production.
type noopVerifier struct{ uid string }

// NewNoopVerifier returns a Verifier that yields a stable dev UID. An empty
// token still fails so middleware's "missing Authorization" path stays
// exercised in tests.
func NewNoopVerifier() Verifier { return &noopVerifier{uid: "dev-local"} }

func (v *noopVerifier) Verify(_ context.Context, idToken string) (string, error) {
	if idToken == "" {
		return "", ErrInvalidToken
	}
	return v.uid, nil
}
