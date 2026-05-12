package debate

import (
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
