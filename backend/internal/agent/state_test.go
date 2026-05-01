package agent

import "testing"

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
			name:  "missing key resolves to empty",
			tmpl:  "before {missing} after",
			state: State{},
			want:  "before  after",
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
			name: "JSON schema example with whitespace after brace passes through",
			// This is the load-bearing case — critic/judge instructions
			// embed JSON examples and must survive substitution intact.
			tmpl:  `Return JSON: { "name": "X", "fallacies": [] }`,
			state: State{"name": "should not appear"},
			want:  `Return JSON: { "name": "X", "fallacies": [] }`,
		},
		{
			name:  "brace followed by digit is not a placeholder",
			tmpl:  "{0invalid} stays literal",
			state: State{"0invalid": "x"},
			want:  "{0invalid} stays literal",
		},
		{
			name:  "pending_interjection empty string",
			tmpl:  "before\n{pending_interjection}\nafter",
			state: State{"pending_interjection": ""},
			want:  "before\n\nafter",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Substitute(tc.tmpl, tc.state)
			if got != tc.want {
				t.Errorf("Substitute(%q):\ngot:  %q\nwant: %q", tc.tmpl, got, tc.want)
			}
		})
	}
}
