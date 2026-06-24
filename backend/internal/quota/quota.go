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
// users untouched. Lost updates under concurrency are prevented by the store's
// transactional read-modify-write, not a version field on this struct: unlike
// debate state — whose read precedes a multi-second LLM call and so needs
// application-level OCC (ADR 0004) — the quota read-modify-write happens wholly
// inside the transaction, which Firestore's own concurrency control covers.
type Account struct {
	UID           string  `firestore:"uid"`
	SpentUSD      float64 `firestore:"spent_usd"`
	SpendLimitUSD float64 `firestore:"spend_limit_usd"`
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
	// transaction or equivalent). costUSD may be negative to refund (ADR 0013).
	Add(ctx context.Context, uid string, costUSD, initialLimitUSD float64) error
	// Reserve atomically charges amountUSD only if it fits the budget, returning
	// ok=true when reserved and ok=false (no write) when spend+amount would
	// exceed the limit. Like Add it seeds a missing account with initialLimitUSD.
	// The check-and-increment is one atomic operation so concurrent callers
	// cannot collectively overshoot the limit (ADR 0013). err is a store failure
	// (callers fail open), distinct from the ok=false budget refusal.
	Reserve(ctx context.Context, uid string, amountUSD, initialLimitUSD float64) (ok bool, err error)
}
