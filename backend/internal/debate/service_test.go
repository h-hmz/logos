package debate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/h-hmz/logos/internal/philosopher"
)

// validModel is an allowlisted model used by gate tests that need to advance
// past the model check to reach the behaviour under test.
const validModel = "z-ai/glm-4.6"

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
	svc := NewService(repo, phils, unlimitedQuota())
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
			MaxWords:     100,
			StepRecords:  make([]StepRecord, completed),
		})
		if err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	seed("d1", "alice", 0)
	if _, err := svc.NextTurn(ctx, "bob", "d1", "", validModel, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner NextTurn err = %v, want ErrForbidden", err)
	}

	seed("d2", "alice", TotalSteps(1))
	_, err := svc.NextTurn(ctx, "alice", "d2", "", validModel, nil)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("owner NextTurn err = %v, want *ValidationError (past ownership gate)", err)
	}
}

// TestNextTurn_InterjectionValidation covers the two pre-generation guards on an
// interjection: it must fit InterjectionMaxLen, and it may only steer a debater
// turn (the moderator intro at step 0 is a referee step). Both reject before any
// model call, so no LLM fake is needed.
func TestNextTurn_InterjectionValidation(t *testing.T) {
	phils := map[string]philosopher.Data{
		"Socrates": {Name: "Socrates", School: "Classical", Identity: "id", KeyThinkers: "k"},
		"Kant":     {Name: "Kant", School: "German Idealism", Identity: "id", KeyThinkers: "k"},
	}
	repo := NewMemoryRepo()
	svc := NewService(repo, phils, unlimitedQuota())
	ctx := context.Background()

	// A fresh debate owned by alice; step 0 (next to run) is the moderator intro.
	if err := repo.Create(ctx, &Persisted{
		ID: "d1", OwnerUID: "alice", DebaterAName: "Socrates", DebaterBName: "Kant",
		NumRounds: 1, MaxWords: 100,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("over-length is rejected", func(t *testing.T) {
		long := strings.Repeat("x", InterjectionMaxLen+1)
		_, err := svc.NextTurn(ctx, "alice", "d1", long, validModel, nil)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("err = %v, want *ValidationError for over-length note", err)
		}
	})

	t.Run("rejected on a referee step", func(t *testing.T) {
		// The next step is the moderator intro — not a debater turn — so even a
		// well-formed note is refused rather than silently dropped.
		_, err := svc.NextTurn(ctx, "alice", "d1", "Press him on free will.", validModel, nil)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("err = %v, want *ValidationError for note on referee step", err)
		}
	})
}

// TestRetryTurn_Gates covers every pre-generation guard on the retry path, each
// of which returns before any model call so no LLM fake is needed: ownership,
// a required step_key, an empty debate with nothing to retry, and a step_key
// that is not the current last record (only the last step is retryable).
func TestRetryTurn_Gates(t *testing.T) {
	phils := map[string]philosopher.Data{
		"Socrates": {Name: "Socrates", School: "Classical", Identity: "id", KeyThinkers: "k"},
		"Kant":     {Name: "Kant", School: "German Idealism", Identity: "id", KeyThinkers: "k"},
	}
	repo := NewMemoryRepo()
	svc := NewService(repo, phils, unlimitedQuota())
	ctx := context.Background()

	seed := func(id, owner string, records ...StepRecord) {
		t.Helper()
		err := repo.Create(ctx, &Persisted{
			ID: id, OwnerUID: owner, DebaterAName: "Socrates", DebaterBName: "Kant",
			NumRounds: 1, MaxWords: 100, StepRecords: records,
		})
		if err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	t.Run("non-owner is forbidden", func(t *testing.T) {
		seed("d1", "alice", StepRecord{Key: "moderator_intro", Content: "Welcome."})
		if _, err := svc.RetryTurn(ctx, "bob", "d1", "moderator_intro", "", nil); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-owner RetryTurn err = %v, want ErrForbidden", err)
		}
	})

	t.Run("missing step_key is rejected", func(t *testing.T) {
		seed("d2", "alice", StepRecord{Key: "moderator_intro", Content: "Welcome."})
		_, err := svc.RetryTurn(ctx, "alice", "d2", "  ", "", nil)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("err = %v, want *ValidationError for missing step_key", err)
		}
	})

	t.Run("empty debate has nothing to retry", func(t *testing.T) {
		seed("d3", "alice") // no completed steps
		_, err := svc.RetryTurn(ctx, "alice", "d3", "moderator_intro", "", nil)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("err = %v, want *ValidationError for empty debate", err)
		}
	})

	t.Run("non-last step is refused", func(t *testing.T) {
		// Last record is socrates_r1; targeting the earlier moderator intro is
		// neither the last step nor a fresh view, and both resolve as ErrNotLastStep.
		seed("d4", "alice",
			StepRecord{Key: "moderator_intro", Content: "Welcome."},
			StepRecord{Key: "socrates_r1", Content: "My opening."},
		)
		if _, err := svc.RetryTurn(ctx, "alice", "d4", "moderator_intro", "", nil); !errors.Is(err, ErrNotLastStep) {
			t.Fatalf("err = %v, want ErrNotLastStep", err)
		}
	})

	t.Run("non-allowlisted model is rejected", func(t *testing.T) {
		// The model gate runs after the last-step check but before any generation,
		// so a bad model on a valid target fails fast with no LLM call.
		seed("d5", "alice", StepRecord{Key: "moderator_intro", Content: "Welcome."})
		_, err := svc.RetryTurn(ctx, "alice", "d5", "moderator_intro", "openai/gpt-4o", nil)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("err = %v, want *ValidationError for non-allowlisted model", err)
		}
	})

	t.Run("missing model is rejected", func(t *testing.T) {
		// Every generation must name a model — there is no sticky default anymore.
		seed("d6", "alice", StepRecord{Key: "moderator_intro", Content: "Welcome."})
		_, err := svc.RetryTurn(ctx, "alice", "d6", "moderator_intro", "", nil)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("err = %v, want *ValidationError for missing model", err)
		}
	})
}

// TestGetSession_SurfacesInterjection verifies a persisted interjection is
// replayed on the turn it shaped, so a client rehydrating the debate sees the
// note against its turn.
func TestGetSession_SurfacesInterjection(t *testing.T) {
	phils := map[string]philosopher.Data{
		"Socrates": {Name: "Socrates", School: "Classical", Identity: "id", KeyThinkers: "k"},
		"Kant":     {Name: "Kant", School: "German Idealism", Identity: "id", KeyThinkers: "k"},
	}
	repo := NewMemoryRepo()
	svc := NewService(repo, phils, unlimitedQuota())
	ctx := context.Background()

	const note = "Press him on whether that assumes free will."
	if err := repo.Create(ctx, &Persisted{
		ID: "d1", OwnerUID: "alice", DebaterAName: "Socrates", DebaterBName: "Kant",
		NumRounds: 1, MaxWords: 100,
		StepRecords: []StepRecord{
			{Key: "moderator_intro", Content: "Welcome."},
			{Key: "socrates_r1", Content: "My opening.", Interjection: note},
		},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	res, err := svc.GetSession(ctx, "d1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got := res.Turns[1].Interjection; got != note {
		t.Fatalf("Turns[1].Interjection = %q, want %q", got, note)
	}
	if got := res.Turns[0].Interjection; got != "" {
		t.Fatalf("Turns[0].Interjection = %q, want empty (no note on that turn)", got)
	}
}
