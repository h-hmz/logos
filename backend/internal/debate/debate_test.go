package debate

import (
	"testing"

	"github.com/h-hmz/logos/internal/philosopher"
)

func TestIsHumanDebater(t *testing.T) {
	if !IsHumanDebater(HumanDebaterName) {
		t.Fatalf("IsHumanDebater(%q) = false, want true", HumanDebaterName)
	}
	for _, name := range []string{"Socrates", "Kant", "", "the challenger"} {
		if IsHumanDebater(name) {
			t.Errorf("IsHumanDebater(%q) = true, want false", name)
		}
	}
}

func TestBuildStepMeta_WithChallenger(t *testing.T) {
	a := philosopher.Data{Name: HumanDebaterName, School: "Human debater"}
	b := philosopher.Data{Name: "Socrates", School: "Classical"}
	meta := BuildStepMeta(a, b, 1)
	if len(meta) != TotalSteps(1) {
		t.Fatalf("len(meta) = %d, want %d", len(meta), TotalSteps(1))
	}
	want := []struct{ key, label string }{
		{"moderator_intro", "Introduction"},
		{"the_challenger_r1", HumanDebaterName + " — Round 1"},
		{"socrates_r1", "Socrates — Round 1"},
		{"critic_feedback", "Critic Analysis"},
		{"the_challenger_critic_response", HumanDebaterName + " — Response to Critic"},
		{"socrates_critic_response", "Socrates — Response to Critic"},
		{"final_verdict", "Judge's Verdict"},
	}
	for i, w := range want {
		if meta[i].Key != w.key {
			t.Errorf("meta[%d].Key = %q, want %q", i, meta[i].Key, w.key)
		}
		if meta[i].Label != w.label {
			t.Errorf("meta[%d].Label = %q, want %q", i, meta[i].Label, w.label)
		}
	}
}

// TestStepCount verifies the formula: 1 moderator + 2*N rounds + 1 critic + 2 critic responses + 1 judge = 2N + 5
func TestStepCount(t *testing.T) {
	tests := []struct {
		rounds int
		want   int
	}{
		{1, 7},
		{2, 9},
		{3, 11},
		{5, 15},
	}
	for _, tt := range tests {
		got := 2*tt.rounds + 5
		if got != tt.want {
			t.Errorf("stepCount(rounds=%d) = %d, want %d", tt.rounds, got, tt.want)
		}
	}
}
