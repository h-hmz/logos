package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/modelcache"
)

// ── GET /philosophers ───────────────────────────────────────────────────────

type philosopherItem struct {
	Name     string `json:"name"`
	School   string `json:"school"`
	Era      string `json:"era,omitempty"`
	Dates    string `json:"dates,omitempty"`
	OneLiner string `json:"one_liner,omitempty"`
}

func (s *Server) listPhilosophers(w http.ResponseWriter, r *http.Request) {
	items := make([]philosopherItem, len(s.philosophers))
	for i, p := range s.philosophers {
		items[i] = philosopherItem{
			Name:     p.Name,
			School:   p.School,
			Era:      p.Era,
			Dates:    p.Dates,
			OneLiner: p.OneLiner,
		}
	}
	writeJSON(w, http.StatusOK, items)
}

// ── POST /suggest-topic ─────────────────────────────────────────────────────

type suggestTopicRequest struct {
	DebaterA string `json:"debater_a"`
	DebaterB string `json:"debater_b"`
}

type suggestTopicResponse struct {
	Topics []string `json:"topics"`
}

func (s *Server) suggestTopic(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req suggestTopicRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	a, okA := s.philMap[req.DebaterA]
	b, okB := s.philMap[req.DebaterB]
	if !okA || !okB {
		writeError(w, http.StatusBadRequest, "Unknown philosopher name")
		return
	}
	if req.DebaterA == req.DebaterB {
		writeError(w, http.StatusBadRequest, "Debaters must be different")
		return
	}

	ctx := r.Context()
	m, err := modelcache.Get(ctx, config.DefaultModel())
	if err != nil {
		slog.ErrorContext(ctx, "suggest-topic: get model error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get model")
		return
	}

	instruction := fmt.Sprintf(`You propose debate topics for a philosophical debate stage.

**Debater A**: %s — %s
%s

**Debater B**: %s — %s
%s

Propose **5 distinct** debate topics (each a question, 6–14 words) that would produce the most intellectually productive clashes between these two thinkers. The topics should:
- Sit at genuine points of disagreement between their traditions.
- Be accessible to a broad audience — no insider jargon.
- Be phrased as neutral questions, not loaded toward either side.
- Cover different axes of disagreement (ethics, politics, metaphysics, human nature, etc.) — do not repeat the same theme.
- Avoid obvious clichés for this pairing.

Respond with **only the 5 topics**, one per line, no numbering, no bullets, no preamble, no quotation marks, no explanation.`,
		a.Name, a.School, a.Identity,
		b.Name, b.School, b.Identity,
	)

	topicAgent, err := llmagent.New(llmagent.Config{
		Name:        "TopicSuggester",
		Description: "Suggests a debate topic for a pair of philosophers.",
		Model:       m,
		Instruction: instruction,
	})
	if err != nil {
		slog.ErrorContext(ctx, "suggest-topic: create agent error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create agent")
		return
	}

	svc := session.InMemoryService()
	createResp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "logos",
		UserID:  "web",
	})
	if err != nil {
		slog.ErrorContext(ctx, "suggest-topic: create session error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	rn, err := runner.New(runner.Config{
		Agent:          topicAgent,
		AppName:        "logos",
		SessionService: svc,
	})
	if err != nil {
		slog.ErrorContext(ctx, "suggest-topic: runner create error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create runner")
		return
	}

	msg := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{genai.NewPartFromText("suggest a topic")},
	}

	var raw string
	for event, runErr := range rn.Run(ctx, "web", createResp.Session.ID(), msg, agent.RunConfig{}) {
		if runErr != nil {
			slog.ErrorContext(ctx, "suggest-topic: runner error", "error", runErr)
			writeError(w, http.StatusInternalServerError, "runner error")
			return
		}
		if event.Content != nil {
			for _, part := range event.Content.Parts {
				if part.Text != "" {
					raw = part.Text
				}
			}
		}
	}

	topics := parseTopicList(raw)
	if len(topics) == 0 {
		writeError(w, http.StatusInternalServerError, "model returned no topics")
		return
	}

	writeJSON(w, http.StatusOK, suggestTopicResponse{Topics: topics})
}

// parseTopicList splits the model output into individual topics, stripping
// leading list markers (1., 1), -, *, •) and surrounding quotes.
func parseTopicList(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, 5)
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		// strip common list markers: "1.", "1)", "-", "*", "•"
		for {
			trimmed := strings.TrimLeft(t, "0123456789")
			if len(trimmed) < len(t) && len(trimmed) > 0 && (trimmed[0] == '.' || trimmed[0] == ')') {
				t = strings.TrimSpace(trimmed[1:])
				continue
			}
			stripped := false
			for _, prefix := range []string{"-", "*", "•"} {
				if strings.HasPrefix(t, prefix) {
					t = strings.TrimSpace(t[len(prefix):])
					stripped = true
					break
				}
			}
			if stripped {
				continue
			}
			break
		}
		t = strings.Trim(t, "\"'“”‘’")
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}
