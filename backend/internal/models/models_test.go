package models

import (
	"testing"
)

func TestAllowed(t *testing.T) {
	cases := map[string]bool{
		"deepseek/deepseek-v4-pro":  true,
		"qwen/qwen3-235b-a22b-2507": true,
		"z-ai/glm-5":                true,
		"":                          false,
		"gpt-4o":                    false,
		"Z-AI/GLM-5":                false, // case-sensitive: IDs are exact provider slugs
		" z-ai/glm-5":               false, // not trimmed here; callers trim before checking
	}
	for id, want := range cases {
		if got := Allowed(id); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", id, got, want)
		}
	}
}
