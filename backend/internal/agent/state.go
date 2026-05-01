// Package agent provides the in-house LLM-agent layer that replaces
// Google ADK. See ADR 0008.
package agent

import "regexp"

// State is the per-request scratchpad that backs {key} substitution
// and OutputKey writes for the debate pipeline. See ADR 0003.
type State map[string]string

// placeholderRe matches {identifier} where identifier starts with a
// letter or underscore. The opening brace must be immediately followed
// by the identifier (no whitespace), so literal JSON braces in the
// critic/judge instructions — which always have whitespace after `{` —
// pass through untouched.
var placeholderRe = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

// Substitute replaces every {key} placeholder in tmpl with state[key].
// Missing keys resolve to the empty string, matching ADK's pre-2026-04-30
// semantics.
func Substitute(tmpl string, state State) string {
	return placeholderRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		return state[match[1:len(match)-1]]
	})
}
