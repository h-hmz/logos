package agent

import (
	"context"
	"testing"
)

// TestRoleFromAgentName is a guard against silent breakage of the metric
// `role` label scheme when one of the well-known agents is renamed.
//
// The cases below mirror the Name values actually constructed in the codebase:
//
//	philosopher.MakeModerator        -> "Moderator"
//	philosopher.MakeCritic           -> "Critic"
//	philosopher.MakeJudge            -> "Judge"
//	tutor.MakeTutor                  -> "Noctua"
//	philosopher.MakePhilosopher      -> opts.AgentName (per-turn, e.g. "socrates_r1", "kant_CriticResponse")
//
// If a rename here lands without a matching switch update in
// roleFromAgentName, the renamed agent silently falls into the "debater"
// bucket — this test catches that at CI time.
func TestRoleFromAgentName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"Moderator", "moderator"},
		{"Critic", "critic"},
		{"Judge", "judge"},
		{"Noctua", "tutor"},
		// Per-turn philosopher agent names — all collapse to "debater":
		{"socrates_r1", "debater"},
		{"kant_r3", "debater"},
		{"kant_CriticResponse", "debater"},
		// Unknown / empty names default to debater (a safe bucket — the
		// philosopher per-turn path is the only thing that produces names
		// not explicitly enumerated above).
		{"", "debater"},
		{"SomethingNew", "debater"},
	}
	for _, c := range cases {
		if got := roleFromAgentName(c.name); got != c.want {
			t.Errorf("roleFromAgentName(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAgentNameContextRoundTrip(t *testing.T) {
	if got := agentNameFromContext(context.Background()); got != "" {
		t.Errorf("expected empty name on bare ctx, got %q", got)
	}
	ctx := withAgentName(context.Background(), "Critic")
	if got := agentNameFromContext(ctx); got != "Critic" {
		t.Errorf("agentNameFromContext after withAgentName: got %q, want %q", got, "Critic")
	}
}
