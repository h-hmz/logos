package debate

import (
	"context"
	"errors"
	"testing"

	"github.com/h-hmz/logos/internal/config"
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

// TestCreateDebate_Models verifies Debater Model resolution: an omitted model
// falls back to the Default Model, a provided authorized model is stamped onto
// the record, and a provided unauthorized model is rejected as a ValidationError.
func TestCreateDebate_Models(t *testing.T) {
	phils := map[string]philosopher.Data{
		"Socrates": {Name: "Socrates", School: "Classical", Identity: "id", KeyThinkers: "k"},
		"Kant":     {Name: "Kant", School: "German Idealism", Identity: "id", KeyThinkers: "k"},
	}
	t.Setenv("MODEL", "default-model")
	if err := config.Load(""); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	svc := NewService(NewMemoryRepo(), phils)
	ctx := context.Background()
	base := CreateDebateParams{Topic: "free will", DebaterA: "Socrates", DebaterB: "Kant"}

	t.Run("omitted falls back to default", func(t *testing.T) {
		res, err := svc.CreateDebate(ctx, "alice", base)
		if err != nil {
			t.Fatalf("CreateDebate: %v", err)
		}
		// Every role seeds to the Default Model when nothing is supplied.
		want := RoleModels{DebaterA: "default-model", DebaterB: "default-model", Moderator: "default-model", Critic: "default-model", Judge: "default-model"}
		if res.Debate.Models != want {
			t.Fatalf("Models = %+v, want %+v", res.Debate.Models, want)
		}
	})

	t.Run("authorized debater models are stamped", func(t *testing.T) {
		in := base
		in.ModelA = "z-ai/glm-4.6"
		in.ModelB = "  qwen/qwen3-235b-a22b-2507  " // surrounding space is trimmed
		res, err := svc.CreateDebate(ctx, "alice", in)
		if err != nil {
			t.Fatalf("CreateDebate: %v", err)
		}
		if res.Debate.Models.DebaterA != "z-ai/glm-4.6" {
			t.Errorf("DebaterA = %q, want %q", res.Debate.Models.DebaterA, "z-ai/glm-4.6")
		}
		if res.Debate.Models.DebaterB != "qwen/qwen3-235b-a22b-2507" {
			t.Errorf("DebaterB = %q, want %q", res.Debate.Models.DebaterB, "qwen/qwen3-235b-a22b-2507")
		}
		// Referee roles still seed to the Default Model (not yet caller-configurable).
		if res.Debate.Models.Moderator != "default-model" || res.Debate.Models.Critic != "default-model" || res.Debate.Models.Judge != "default-model" {
			t.Errorf("referee models = (%q, %q, %q), want all %q",
				res.Debate.Models.Moderator, res.Debate.Models.Critic, res.Debate.Models.Judge, "default-model")
		}
	})

	t.Run("unauthorized model is rejected", func(t *testing.T) {
		in := base
		in.ModelA = "openai/gpt-4o"
		_, err := svc.CreateDebate(ctx, "alice", in)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("CreateDebate err = %v, want *ValidationError", err)
		}
	})
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
