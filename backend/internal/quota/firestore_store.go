package quota

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FirestoreStore persists per-user spend to a Firestore collection, one
// document per UID. Suitable for Cloud Run, where process-local counters can't
// survive scale events. Spend is incremented inside a transaction so concurrent
// writers on the same instance (or across instances) don't lose updates.
type FirestoreStore struct {
	client *firestore.Client
	coll   *firestore.CollectionRef
}

// NewFirestoreStore constructs a FirestoreStore bound to the given project,
// database, and collection. Use "(default)" / "" for the default database.
func NewFirestoreStore(ctx context.Context, projectID, databaseID, collection string) (*FirestoreStore, error) {
	if projectID == "" {
		return nil, fmt.Errorf("quota firestore: projectID is required")
	}
	if collection == "" {
		return nil, fmt.Errorf("quota firestore: collection is required")
	}
	if databaseID == "" {
		databaseID = firestore.DefaultDatabaseID
	}
	client, err := firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	if err != nil {
		return nil, fmt.Errorf("quota firestore: new client: %w", err)
	}
	return &FirestoreStore{client: client, coll: client.Collection(collection)}, nil
}

func (s *FirestoreStore) Get(ctx context.Context, uid string) (*Account, error) {
	snap, err := s.coll.Doc(uid).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("quota firestore: get %q: %w", uid, err)
	}
	var a Account
	if err := snap.DataTo(&a); err != nil {
		return nil, fmt.Errorf("quota firestore: decode %q: %w", uid, err)
	}
	return &a, nil
}

// Add increments the user's spend by costUSD inside a transaction. A missing
// document is created with its SpendLimitUSD set to initialLimitUSD; an existing
// document keeps its stored limit (initialLimitUSD is ignored), so changing the
// configured default never moves an existing user's ceiling. The version field
// is advanced on every write to support optimistic concurrency (ADR 0004); the
// transaction itself guarantees the read-modify-write is not lost under
// contention.
func (s *FirestoreStore) Add(ctx context.Context, uid string, costUSD, initialLimitUSD float64) error {
	docRef := s.coll.Doc(uid)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(docRef)
		if err != nil && status.Code(err) != codes.NotFound {
			return fmt.Errorf("tx get: %w", err)
		}
		acct := Account{UID: uid, SpendLimitUSD: initialLimitUSD}
		if snap.Exists() {
			if err := snap.DataTo(&acct); err != nil {
				return fmt.Errorf("tx decode: %w", err)
			}
		}
		acct.UID = uid
		acct.SpentUSD += costUSD
		acct.Version++
		if err := tx.Set(docRef, acct); err != nil {
			return fmt.Errorf("tx set: %w", err)
		}
		return nil
	})
}
