package debate

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FirestoreRepo persists debates to a Firestore collection. Suitable for
// Cloud Run deployments where process-local state cannot survive scale
// events.
type FirestoreRepo struct {
	client *firestore.Client
	coll   *firestore.CollectionRef
}

// NewFirestoreRepo constructs a FirestoreRepo bound to the given project,
// database, and collection. Use "(default)" for the default Firestore
// database.
func NewFirestoreRepo(ctx context.Context, projectID, databaseID, collection string) (*FirestoreRepo, error) {
	if projectID == "" {
		return nil, fmt.Errorf("firestore: projectID is required")
	}
	if collection == "" {
		return nil, fmt.Errorf("firestore: collection is required")
	}
	if databaseID == "" {
		databaseID = firestore.DefaultDatabaseID
	}
	client, err := firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	if err != nil {
		return nil, fmt.Errorf("firestore: new client: %w", err)
	}
	return &FirestoreRepo{client: client, coll: client.Collection(collection)}, nil
}

func (r *FirestoreRepo) Get(ctx context.Context, id string) (*Persisted, error) {
	snap, err := r.coll.Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("firestore: get %q: %w", id, err)
	}
	var p Persisted
	if err := snap.DataTo(&p); err != nil {
		return nil, fmt.Errorf("firestore: decode %q: %w", id, err)
	}
	return &p, nil
}

func (r *FirestoreRepo) Create(ctx context.Context, p *Persisted) error {
	if p.ID == "" {
		return fmt.Errorf("firestore: Persisted.ID is required")
	}
	cp := p.Clone()
	cp.Version = 1
	if _, err := r.coll.Doc(p.ID).Create(ctx, cp); err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return ErrAlreadyExists
		}
		return fmt.Errorf("firestore: create %q: %w", p.ID, err)
	}
	p.Version = 1
	return nil
}

// Update applies an optimistic, version-checked write inside a Firestore
// transaction. Returns ErrNotFound if the document is missing, and
// ErrVersionConflict if another writer has advanced the version since p
// was loaded.
func (r *FirestoreRepo) Update(ctx context.Context, p *Persisted) error {
	if p.ID == "" {
		return fmt.Errorf("firestore: Persisted.ID is required")
	}
	docRef := r.coll.Doc(p.ID)

	var committedVersion int64
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(docRef)
		if status.Code(err) == codes.NotFound {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("tx get: %w", err)
		}
		var current Persisted
		if err := snap.DataTo(&current); err != nil {
			return fmt.Errorf("tx decode: %w", err)
		}
		if current.Version != p.Version {
			return ErrVersionConflict
		}
		next := p.Clone()
		next.Version = current.Version + 1
		committedVersion = next.Version
		if err := tx.Set(docRef, next); err != nil {
			return fmt.Errorf("tx set: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	p.Version = committedVersion
	return nil
}
