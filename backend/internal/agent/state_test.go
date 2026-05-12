package agent

import (
	"strings"
	"testing"
)

func TestSubstitute(t *testing.T) {
	cases := []struct {
		name  string
		tmpl  string
		state State
		want  string
	}{
		{
			name:  "simple key",
			tmpl:  "Topic: {topic}",
			state: State{"topic": "free will"},
			want:  "Topic: free will",
		},
		{
			name:  "underscored identifier with digit",
			tmpl:  "Last: {john_stuart_mill_r2}",
			state: State{"john_stuart_mill_r2": "Liberty is the only test of progress."},
			want:  "Last: Liberty is the only test of progress.",
		},
		{
			name:  "multiple keys in one template",
			tmpl:  "{topic} | {a} vs {b}",
			state: State{"topic": "X", "a": "Plato", "b": "Aristotle"},
			want:  "X | Plato vs Aristotle",
		},
		{
			name:  "brace followed by digit is not a placeholder",
			tmpl:  "{0invalid} stays literal",
			state: State{},
			want:  "{0invalid} stays literal",
		},
		{
			name:  "empty-string value substitutes (key present, value empty)",
			tmpl:  "before\n{optional_slot}\nafter",
			state: State{"optional_slot": ""},
			want:  "before\n\nafter",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := substitute(tc.tmpl, tc.state)
			if err != nil {
				t.Fatalf("substitute(%q): unexpected error: %v", tc.tmpl, err)
			}
			if got != tc.want {
				t.Errorf("substitute(%q):\ngot:  %q\nwant: %q", tc.tmpl, got, tc.want)
			}
		})
	}
}

func TestSubstituteMissingKeyErrors(t *testing.T) {
	_, err := substitute("before {missing} after", State{})
	if err == nil {
		t.Fatal("substitute with missing key: expected error, got nil")
	}
}

// Pin that all missing keys are reported in one error so a future
// early-return refactor doesn't silently regress debuggability.
func TestSubstituteMissingKeyErrorListsAll(t *testing.T) {
	_, err := substitute("{a} {b} {c}", State{"b": "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "a") || !strings.Contains(msg, "c") {
		t.Errorf("error should mention both missing keys, got: %v", err)
	}
}
