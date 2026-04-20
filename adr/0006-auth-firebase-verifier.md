# ADR 0006: Authentication via Firebase ID tokens behind a Verifier interface

Date: 2026-04-20
Status: Accepted

## Context

The backend proxies paid LLM and TTS APIs on a hobbyist budget. Without
authentication on money-spending routes, three problems compound:

- Every route is an open invitation to drain the LLM / TTS budget; the
  only remaining defences (provider-side spend caps, Cloud Run
  `--max-instances`) are last resorts, not first ones.
- Per-user quotas need a stable key. Without a UID there is nothing to
  index a `quotas/{uid}` document on, so the cheapest form of abuse
  control — "user X has burned their daily tokens" — is not
  expressible.
- Request-scoped logs (ADR 0005) can correlate by `request_id` but not
  by caller. When someone reports a bad debate, ops cannot pivot from
  "this user" to "their recent requests".

The app runs on Cloud Run + Firestore and the frontend is a hobby-scale
SPA. Signup friction must stay near zero or users bounce; operational
overhead must stay near zero or maintenance collapses.

## Decision

**1. Firebase Auth ID tokens, verified in middleware.** The backend
trusts tokens issued by Firebase Auth for this project's GCP project
and verified against Google's rotating public keys by the Firebase
Admin SDK. Anonymous auth is acceptable: it still yields a stable UID,
which is what the quota layer needs.

**2. A `Verifier` interface in a new `internal/auth` package.**

```go
type Verifier interface {
    Verify(ctx context.Context, idToken string) (uid string, err error)
}
```

Two implementations ship: `NewFirebaseVerifier` (production) and
`NewNoopVerifier` (local dev and tests). The seam exists so:

- Tests run without a network call or Firebase project.
- Local development via `AUTH_BACKEND=none` skips the Admin SDK init
  entirely, keeping `go run` fast and offline-friendly.
- Swapping providers later (self-hosted JWT, Auth0, custom SSO) is a
  one-file change.

**3. Route-group middleware, not global middleware.** `authMiddleware`
is installed on a chi `Group` containing only the protected
routes; public routes sit outside it:

- Public: `/healthz`, `/readyz`, `/models`, `/philosophers`.
- Protected: `/debates`, `/debates/{id}/next`, `/debates/{id}/interject`,
  `/debates/{id}/tts`, `/suggest-topic`.

Health checks must never trigger auth work: probes and bots hammer
them at high frequency and they must cost nothing. `/models` and
`/philosophers` are cheap static reads that the SPA calls before the
user signs in; putting them behind auth would force a login just to
render the landing page.

**4. UID propagated via `context.Context`, logged automatically.** The
middleware stashes the verified UID under an unexported `ctxKeyUID`.
`server.UIDFromContext(ctx)` exposes it to handlers; the root
`contextHandler` in `main.go` (ADR 0005) stamps `uid` on every log
record when present, mirroring how `request_id` flows.

## Consequences

### Positive

- A compromised or absent frontend cannot call any paid route. The
  worst case for an unauthenticated attacker collapses from "burn the
  budget" to "consume static JSON".
- The UID is the natural key for a future Firestore quota
  middleware, so this ADR unblocks per-user rate limiting without
  further design.
- Ops can pivot log queries on `uid=...` with no new plumbing.
- The noop verifier makes tests and local dev trivially fast: no
  network call, no Firebase project, no secrets on disk.

### Negative

- The first `VerifyIDToken` call per container fetches Google's
  signing keys (~100–200ms HTTPS round-trip). Keys are cached ~6h
  in-process; at `--concurrency 200` this cost is amortised across
  ~200 concurrent users and irrelevant.
- Firebase Admin SDK pulls a non-trivial dependency tree
  (`firebase.google.com/go/v4` + transitive Google APIs). Acceptable
  since we are already on GCP; would be revisited if we ever
  multi-cloud.
- The frontend now owes an `Authorization: Bearer <idToken>` header on
  every protected call. This is one-time wiring around the Firebase
  client SDK but must land before this change ships to users.
- `AUTH_BACKEND=none` is a footgun if set in production. The startup
  path logs a `WARN` and the variable is undocumented in user-facing
  config; a deploy-time check could be added if drift becomes a
  concern.

## Non-goals / deferred

- **Per-user Firestore quotas.** The whole point of authenticating is
  to key quotas on UID, but the quota middleware itself is out of
  scope here to keep the change reviewable. It will read
  `UIDFromContext` and gate on a `quotas/{uid}` transaction.
- **Role / tier enforcement.** Today everyone authenticated gets the
  same access. A `custom claims`-based tier ("free" vs "paid") can
  layer onto the same `Verifier` result later.
- **Token revocation checks.** `VerifyIDToken` by default does not
  check the revocation list. `VerifyIDTokenAndCheckRevoked` is one
  flag away if we ever need to force-log-out compromised users; the
  added latency is not justified today.
- **Rate limiting by IP.** Useful as a belt to the UID-quota braces,
  especially for the sign-up flow itself. Deferred to an edge layer
  (e.g. Cloud Armor) rather than in-process.
- **Frontend integration.** The client must attach ID tokens on
  protected calls. Tracked separately; backend is intentionally ready
  before the frontend starts sending headers so the cutover is a
  frontend-only deploy.

## Implementation

- [backend/internal/auth/auth.go](../backend/internal/auth/auth.go) —
  new package; `Verifier` interface, `NewFirebaseVerifier`,
  `NewNoopVerifier`, `ErrInvalidToken`.
- [backend/internal/server/middleware.go](../backend/internal/server/middleware.go) —
  `authMiddleware`, `ctxKeyUID`, `UIDFromContext`.
- [backend/internal/server/routes.go](../backend/internal/server/routes.go) —
  public / protected split via `r.Group`; `/healthz` + `/readyz`
  added; `Authorization` allowed in CORS.
- [backend/main.go](../backend/main.go) — `buildVerifier(ctx)` selects
  the implementation from `AUTH_BACKEND` (`firebase`|`none`, default
  `firebase`); `contextHandler` now stamps `uid` alongside
  `request_id`.
- [backend/internal/server/auth_test.go](../backend/internal/server/auth_test.go) —
  401 paths (missing header, malformed header, empty token, rejected
  token), public-route bypass, UID propagation.
- [backend/internal/server/server_test.go](../backend/internal/server/server_test.go) —
  test helper wraps the router to inject a default bearer so existing
  handler tests continue to exercise protected routes.
