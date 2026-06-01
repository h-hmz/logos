// Package quota enforces a per-user (Firebase UID) lifetime budget of
// generation cost in USD. A "generation" is one debate turn or tutor reply;
// its cost is the provider-billed amount (OpenRouter's usage.cost), known only
// after the call completes. See ADR 0009.
//
// The Store persists each user's accumulated spend and their ceiling. The
// ceiling assigned to a brand-new account is a single configured default
// (uniform for all users) — once an account exists, its own stored
// SpendLimitUSD governs, so changing the config later does not move existing
// users' ceilings. InFlight serializes a single user's concurrent generations
// within one process. Enforcement itself lives in debate.Service, which owns
// the lock→budget→generate→charge sequence.
package quota

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Store.Get when a UID has no recorded spend yet.
// Callers treat it as zero spend, not as a failure.
var ErrNotFound = errors.New("quota account not found")

// Account is the persisted quota record for one user. SpentUSD accumulates the
// actual cost of every completed generation and never resets (ADR 0009).
// SpendLimitUSD is the user's ceiling, snapshotted from configuration when the
// account is first created and authoritative thereafter — the config default
// only seeds new accounts, so raising or lowering it later leaves existing
// users untouched. Version backs the optimistic, version-checked write used to
// avoid lost updates under concurrency (same approach as debate state, ADR 0004).
type Account struct {
	UID           string  `firestore:"uid"`
	SpentUSD      float64 `firestore:"spent_usd"`
	SpendLimitUSD float64 `firestore:"spend_limit_usd"`
	Version       int64   `firestore:"version"`
}

// Store persists per-user quota accounts. Implementations: MemoryStore
// (in-process, for dev/tests) and FirestoreStore (Cloud Run).
type Store interface {
	// Get returns the account for uid, or ErrNotFound if the user has no
	// account yet.
	Get(ctx context.Context, uid string) (*Account, error)
	// Add atomically increments the user's spend by costUSD. When the account
	// does not yet exist it is created with its SpendLimitUSD set to
	// initialLimitUSD (the caller's configured ceiling for a new user); the
	// limit is only applied on creation and ignored for existing accounts.
	// Implementations must be safe against concurrent writers (version-checked
	// transaction or equivalent).
	Add(ctx context.Context, uid string, costUSD, initialLimitUSD float64) error
}
