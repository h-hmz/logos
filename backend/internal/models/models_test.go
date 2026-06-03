package models

import (
	"sort"
	"testing"
)

func TestAllowed(t *testing.T) {
	cases := map[string]bool{
		"deepseek/deepseek-v4-pro":  true,
		"qwen/qwen3-235b-a22b-2507": true,
		"z-ai/glm-4.6":              true,
		"":                          false,
		"gpt-4o":                    false,
		"Z-AI/GLM-4.6":              false, // case-sensitive: IDs are exact provider slugs
		" z-ai/glm-4.6":             false, // not trimmed here; callers trim before checking
	}
	for id, want := range cases {
		if got := Allowed(id); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestList(t *testing.T) {
	got := List()
	want := []string{
		"deepseek/deepseek-v4-pro",
		"qwen/qwen3-235b-a22b-2507",
		"z-ai/glm-4.6",
	}
	if len(got) != len(want) {
		t.Fatalf("List() returned %d ids, want %d: %v", len(got), len(want), got)
	}
	// List is documented to return sorted ids.
	if !sort.StringsAreSorted(got) {
		t.Errorf("List() not sorted: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("List()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Every listed id must itself be Allowed (List and Allowed share one source).
	for _, id := range got {
		if !Allowed(id) {
			t.Errorf("List() returned %q but Allowed(%q) is false", id, id)
		}
	}
}
