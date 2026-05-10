package philosopher

import (
	"fmt"
	"strings"
	"text/template"
)

// render parses tmpl and executes it against data, returning the rendered string.
// Used to keep prompt templates readable (named fields beat positional %s).
func render(name, tmpl string, data any) (string, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s template: %w", name, err)
	}
	return buf.String(), nil
}
