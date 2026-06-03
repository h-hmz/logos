package debate

import (
	"context"
	"errors"
	"testing"

	"github.com/h-hmz/logos/internal/philosopher"
)

func TestRoleForStep(t *testing.T) {
	a := philosopher.Data{Name: "Socrates"}
	b := philosopher.Data{Name: "Kant"}
	cases := map[string]string{
		"moderator_intro":      "moderator",
		"critic_feedback":      "critic",
		"final_verdict":        "judge",
		"socrates_r1":          "a",
		"kant_r2":              "b",
		"kant_critic_response": "b",
		"unknown":              "moderator",
	}
	for key, want := range cases {
		if got := RoleForStep(key, a, b); got != want {
			t.Fatalf("RoleForStep(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestNextTurn_OwnershipGate verifies only the owner may advance a debate: a
// non-owner is refused with ErrForbidden before any generation work, while the
// owner clears the gate (here tripping the already-complete guard, which proves
// the request advanced past ownership without reaching the model).
func TestNextTurn_OwnershipGate(t *testing.T) {
	phils := map[string]philosopher.Data{
		"Socrates": {Name: "Socrates", School: "Classical", Identity: "id", KeyThinkers: "k"},
		"Kant":     {Name: "Kant", School: "German Idealism", Identity: "id", KeyThinkers: "k"},
	}
	repo := NewMemoryRepo()
	svc := NewService(repo, phils)
	ctx := context.Background()

	// completed sets how many pipeline steps have run; progress is derived from
	// StepRecords length (p.Current()), so we seed that many placeholder records.
	seed := func(id, owner string, completed int) {
		t.Helper()
		err := repo.Create(ctx, &Persisted{
			ID:           id,
			OwnerUID:     owner,
			DebaterAName: "Socrates",
			DebaterBName: "Kant",
			NumRounds:    1,
			ModelID:      "test-model",
			MaxWords:     100,
			StepRecords:  make([]StepRecord, completed),
		})
		if err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	seed("d1", "alice", 0)
	if _, err := svc.NextTurn(ctx, "bob", "d1", nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner NextTurn err = %v, want ErrForbidden", err)
	}

	seed("d2", "alice", TotalSteps(1))
	_, err := svc.NextTurn(ctx, "alice", "d2", nil)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("owner NextTurn err = %v, want *ValidationError (past ownership gate)", err)
	}
}
