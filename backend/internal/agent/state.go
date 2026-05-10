// Package agent provides the in-house LLM-agent layer that replaces
// Google ADK. See ADR 0008.
package agent

import (
	"fmt"
	"regexp"
)

// State is the per-request scratchpad that backs {key} substitution
// and OutputKey writes for the debate pipeline. See ADR 0003.
type State map[string]string

// placeholderRe matches {identifier} where identifier starts with a
// letter or underscore. The opening brace must be immediately followed
// by an identifier character — anything else (whitespace, digit,
// punctuation) leaves the brace as a literal.
var placeholderRe = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

// substitute replaces every {key} placeholder in tmpl with state[key].
func substitute(tmpl string, state State) (string, error) {
	var missing []string
	out := placeholderRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		key := match[1 : len(match)-1]
		v, ok := state[key]
		if !ok {
			missing = append(missing, key)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("substitute: missing state keys: %v", missing)
	}
	return out, nil
}
