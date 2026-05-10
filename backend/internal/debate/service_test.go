package debate

import (
	"strings"
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

func TestFormatInterjection(t *testing.T) {
	if formatInterjection("  ") != "" {
		t.Fatalf("whitespace-only should format to empty string")
	}
	got := formatInterjection("first\nsecond")
	if !strings.Contains(got, "Audience Challenge") {
		t.Fatalf("missing header: %q", got)
	}
	if !strings.Contains(got, "> first") || !strings.Contains(got, "> second") {
		t.Fatalf("multi-line quoting broken: %q", got)
	}
}
