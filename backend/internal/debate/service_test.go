package debate

import (
	"context"
	"strings"
	"testing"

	"github.com/h-hmz/logos/internal/philosopher"
)

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{`prose before {"x":"y"} end`, `{"x":"y"}`},
		{`{"a":"has } brace","b":2}`, `{"a":"has } brace","b":2}`},
		{`no json here`, ``},
		{`{"a":1`, ``},
	}
	for _, c := range cases {
		if got := extractJSON(c.in); got != c.want {
			t.Fatalf("extractJSON(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

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

func TestRoleForStep_Challenger(t *testing.T) {
	a := philosopher.Data{Name: HumanDebaterName}
	b := philosopher.Data{Name: "Kant"}
	cases := map[string]string{
		"the_challenger_r1":              "a",
		"the_challenger_r2":              "a",
		"the_challenger_critic_response": "a",
		"kant_r1":                        "b",
		"kant_critic_response":           "b",
	}
	for key, want := range cases {
		if got := RoleForStep(key, a, b); got != want {
			t.Errorf("RoleForStep(%q) = %q, want %q", key, got, want)
		}
	}
}

// challengerDebate creates an in-memory debate with The Challenger as debater A
// at the given Current step index. Returns the service, repo, and debate ID.
// No LLM config is needed — human steps are resolved before Hydrate is called.
func challengerDebate(t *testing.T, current int) (*Service, *MemoryRepo, string) {
	t.Helper()
	philMap := map[string]philosopher.Data{
		HumanDebaterName: {Name: HumanDebaterName, School: "Human debater"},
		"Socrates":        {Name: "Socrates", School: "Classical"},
	}
	repo := NewMemoryRepo()
	p := &Persisted{
		ID:           "human-d1",
		Topic:        "What is knowledge?",
		DebaterAName: HumanDebaterName,
		DebaterBName: "Socrates",
		NumRounds:    1,
		ModelID:      "test-model",
		MaxWords:     100,
		Current:      current,
		StepOutputs:  map[string]string{"moderator_intro": "The debate begins."},
	}
	if err := repo.Create(context.Background(), p); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}
	return NewService(repo, philMap), repo, p.ID
}

func TestNextTurn_HumanStep_NoInput(t *testing.T) {
	svc, repo, id := challengerDebate(t, 1) // step 1 = the_challenger_r1
	res, err := svc.NextTurn(context.Background(), id, "", nil)
	if err != nil {
		t.Fatalf("NextTurn: %v", err)
	}
	if !res.AwaitingHuman {
		t.Fatalf("AwaitingHuman = false, want true")
	}
	if res.Content != "" {
		t.Fatalf("Content = %q, want empty", res.Content)
	}
	// Current must not advance.
	got, _ := repo.Get(context.Background(), id)
	if got.Current != 1 {
		t.Fatalf("Current = %d, want 1 (must not advance on no-input)", got.Current)
	}
}

func TestNextTurn_HumanStep_CurrentDoesNotAdvance(t *testing.T) {
	svc, _, id := challengerDebate(t, 1)
	if _, err := svc.NextTurn(context.Background(), id, "", nil); err != nil {
		t.Fatalf("NextTurn: %v", err)
	}
	// Second call with no input must still return AwaitingHuman — idempotent.
	res2, err := svc.NextTurn(context.Background(), id, "", nil)
	if err != nil {
		t.Fatalf("NextTurn (2nd): %v", err)
	}
	if !res2.AwaitingHuman {
		t.Fatalf("second call: AwaitingHuman = false, want true — Current advanced unexpectedly")
	}
}

func TestNextTurn_HumanStep_WithInput(t *testing.T) {
	svc, repo, id := challengerDebate(t, 1)
	const arg = "Knowledge requires justified true belief."
	res, err := svc.NextTurn(context.Background(), id, arg, nil)
	if err != nil {
		t.Fatalf("NextTurn: %v", err)
	}
	if res.AwaitingHuman {
		t.Fatalf("AwaitingHuman = true, want false")
	}
	if res.Content != arg {
		t.Fatalf("Content = %q, want %q", res.Content, arg)
	}
	// Verify repo state: Current advanced, input stored under the step key.
	got, _ := repo.Get(context.Background(), id)
	if got.Current != 2 {
		t.Fatalf("Current = %d, want 2", got.Current)
	}
	if got.StepOutputs["the_challenger_r1"] != arg {
		t.Fatalf("StepOutputs[the_challenger_r1] = %q, want %q", got.StepOutputs["the_challenger_r1"], arg)
	}
}

func TestNextTurn_HumanStep_WhitespaceInput(t *testing.T) {
	svc, _, id := challengerDebate(t, 1)
	res, err := svc.NextTurn(context.Background(), id, "   \t\n  ", nil)
	if err != nil {
		t.Fatalf("NextTurn: %v", err)
	}
	if !res.AwaitingHuman {
		t.Fatalf("AwaitingHuman = false for whitespace input, want true")
	}
}

func TestNextTurn_HumanStep_CriticResponse(t *testing.T) {
	// Step 4 with numRounds=1 is the_challenger_critic_response — also a human step.
	svc, _, id := challengerDebate(t, 4)
	res, err := svc.NextTurn(context.Background(), id, "", nil)
	if err != nil {
		t.Fatalf("NextTurn: %v", err)
	}
	if !res.AwaitingHuman {
		t.Fatalf("critic response step: AwaitingHuman = false, want true")
	}
	if res.Role != "a" {
		t.Fatalf("Role = %q, want %q", res.Role, "a")
	}
}

func TestNextTurn_HumanStep_AsDebaterB(t *testing.T) {
	// The Challenger as debater B: step 2 = the_challenger_r1 (b goes second).
	philMap := map[string]philosopher.Data{
		"Socrates":        {Name: "Socrates", School: "Classical"},
		HumanDebaterName: {Name: HumanDebaterName, School: "Human debater"},
	}
	repo := NewMemoryRepo()
	p := &Persisted{
		ID:           "human-d2",
		Topic:        "t",
		DebaterAName: "Socrates",
		DebaterBName: HumanDebaterName,
		NumRounds:    1,
		ModelID:      "test-model",
		MaxWords:     100,
		Current:      2, // step 2 = the_challenger_r1 when Challenger is B
		StepOutputs:  map[string]string{"moderator_intro": "begins", "socrates_r1": "socrates arg"},
	}
	if err := repo.Create(context.Background(), p); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}
	svc := NewService(repo, philMap)
	res, err := svc.NextTurn(context.Background(), "human-d2", "", nil)
	if err != nil {
		t.Fatalf("NextTurn: %v", err)
	}
	if !res.AwaitingHuman {
		t.Fatalf("AwaitingHuman = false, want true")
	}
	if res.Role != "b" {
		t.Fatalf("Role = %q, want %q", res.Role, "b")
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
