package philosopher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/h-hmz/logos/internal/agent"
	"gopkg.in/yaml.v3"
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

// Data holds the fields loaded from a philosopher YAML file.
type Data struct {
	Name             string `yaml:"name"              json:"name"`
	School           string `yaml:"school"            json:"school"`
	Era              string `yaml:"era"               json:"era"`
	Dates            string `yaml:"dates"             json:"dates"`
	OneLiner         string `yaml:"one_liner"         json:"one_liner"`
	Identity         string `yaml:"identity"`
	KeyThinkers      string `yaml:"key_thinkers"`
	EpistemicSources string `yaml:"epistemic_sources,omitempty"`
	// TTSVoice is the Gemini voice id for this philosopher (e.g. "Kore",
	// "Rasalgethi"). Empty string falls back to the package default.
	TTSVoice string `yaml:"tts_voice,omitempty" json:"-"`
	TTSStyle string `yaml:"tts_style,omitempty" json:"-"`
}

var keyRe = regexp.MustCompile(`[^a-z0-9]+`)

// Key normalizes a display name to a safe identifier for state keys and agent names.
// "John Stuart Mill" → "john_stuart_mill", "Al-Ghazali" → "al_ghazali"
func Key(name string) string {
	return strings.Trim(keyRe.ReplaceAllString(strings.ToLower(name), "_"), "_")
}

// debateRules returns the shared rules block injected into every philosopher prompt.
func debateRules(maxWords int) string {
	return fmt.Sprintf(`## Debate Rules
- Ground your arguments in your philosophical tradition.
- Reference key thinkers when relevant, but engage with the logic — don't just cite names.
- Respond directly to your opponent's last point before advancing your own.
- Acknowledge strong points from the other side while defending your position.
- Avoid ad hominem attacks — focus on ideas.
- Keep your response focused and under %d words.
`, maxWords)
}

// LoadAllPhilosophers reads every .yaml file in dir and returns the parsed philosopher data.
func LoadAllPhilosophers(dir string) ([]Data, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no philosopher YAML files found in %s", dir)
	}

	sort.Strings(matches)

	var philosophers []Data
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var d Data
		if err := yaml.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if d.Name == "" || d.School == "" || d.Identity == "" || d.KeyThinkers == "" {
			return nil, fmt.Errorf("%s is missing required fields (name, school, identity, key_thinkers)", filepath.Base(path))
		}
		philosophers = append(philosophers, d)
	}
	return philosophers, nil
}

// MakePhilosopherOpts holds the parameters for creating a philosopher agent.
type MakePhilosopherOpts struct {
	Data                Data
	AgentName           string
	OutputKey           string
	Situation           string
	OpponentArgKey      string // state key of opponent's last argument (empty if none)
	CriticKey           string // state key of critic feedback (empty if none)
	ModelID             string
	MaxWords            int
	IncludeInterjection bool // if true, prompt reads {pending_interjection} from session state
}

// InterjectionStateKey is the session-state key read by philosopher turns that
// opted into IncludeInterjection. The server writes a pre-formatted block here
// when the user interjects, then clears it after the next turn consumes it.
const InterjectionStateKey = "pending_interjection"

const philosopherTemplate = `You are **{{.Name}}**, a philosopher representing **{{.School}}**.

## Your Identity
{{.Identity}}
## Key Thinkers
{{.KeyThinkers}}
{{.EpistemicBlock}}{{.DebateRules}}
## Debate Topic
{topic}

## Your Task: {{.Situation}}
{{.ContextBlock}}

Speak in first person as {{.Name}}. Be passionate but rigorous.

## Expressive Delivery
Your reply will be spoken aloud by Google's Gemini TTS, which
supports a wide vocabulary of bracketed delivery cues — short
lowercase tokens written inline, e.g. [excited], [whispers],
[sighs], [laughs softly], [emphatic], [pause], [amused], [scoffs],
[wearily], [thoughtful], and many more in the same shape. Pick
whichever cue genuinely fits the emotional beat; you are not
restricted to a fixed list.

Constraints on use:
- Brackets only — never parentheses.
- Each cue is a short bracketed token (1–4 lowercase words). Do not
  write long stage directions inside brackets.
- Cues are instructions to the narrator and are never read aloud.
- Plain prose remains the default; cues are seasoning, not structure.
`

// MakePhilosopher creates an agent.Agent for a single debate turn.
func MakePhilosopher(ctx context.Context, opts MakePhilosopherOpts) (*agent.Agent, error) {
	d := opts.Data

	var epistemicBlock string
	if d.EpistemicSources != "" {
		epistemicBlock = fmt.Sprintf("\n## Epistemic Foundations\n%s\n", d.EpistemicSources)
	}

	var contextParts []string
	if opts.OpponentArgKey != "" {
		contextParts = append(contextParts, fmt.Sprintf("## Your Opponent's Last Argument\n{%s}", opts.OpponentArgKey))
	}
	if opts.CriticKey != "" {
		contextParts = append(contextParts, fmt.Sprintf("## Critic Feedback\n{%s}", opts.CriticKey))
	}
	if opts.IncludeInterjection {
		contextParts = append(contextParts, fmt.Sprintf("{%s}", InterjectionStateKey))
	}
	contextBlock := strings.Join(contextParts, "\n\n")

	instruction, err := render("philosopher", philosopherTemplate, struct {
		Name           string
		School         string
		Identity       string
		KeyThinkers    string
		EpistemicBlock string
		DebateRules    string
		Situation      string
		ContextBlock   string
	}{
		Name:           d.Name,
		School:         d.School,
		Identity:       d.Identity,
		KeyThinkers:    d.KeyThinkers,
		EpistemicBlock: epistemicBlock,
		DebateRules:    debateRules(opts.MaxWords),
		Situation:      opts.Situation,
		ContextBlock:   contextBlock,
	})
	if err != nil {
		return nil, err
	}

	m, err := agent.Get(ctx, opts.ModelID)
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", opts.ModelID, err)
	}

	a, err := agent.New(agent.Config{
		Name:                opts.AgentName,
		Model:               m,
		Instruction:         instruction,
		OutputKey:           opts.OutputKey,
		MaxCompletionTokens: max(opts.MaxWords*3, 4096),
	})
	if err != nil {
		return nil, fmt.Errorf("create agent %s: %w", opts.AgentName, err)
	}
	return a, nil
}
