package agent

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/openai/openai-go"
)

// TestCostExtractionFromUsageChunk locks in how the generation cost is pulled
// out of an OpenRouter final stream chunk. The `cost` field is not part of the
// OpenAI schema, so openai-go parks it in Usage.JSON.ExtraFields — where it
// always reports Valid()==false. generateOnce must therefore gate on Raw(),
// not Valid(); this test guards against reintroducing a Valid() check.
func TestCostExtractionFromUsageChunk(t *testing.T) {
	// Real OpenRouter final chunk (qwen/qwen3-235b, DeepInfra).
	const raw = `{"id":"gen-1","object":"chat.completion.chunk","created":1779227392,` +
		`"model":"qwen/qwen3-235b-a22b-07-25","provider":"DeepInfra",` +
		`"choices":[{"index":0,"delta":{"content":"","role":"assistant"},"finish_reason":"length"}],` +
		`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cost":0.00000121,` +
		`"is_byok":false,"cost_details":{"upstream_inference_cost":0.00000121}}}`

	var chunk openai.ChatCompletionChunk
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		t.Fatalf("unmarshal chunk: %v", err)
	}

	costRaw := chunk.Usage.JSON.ExtraFields["cost"].Raw()
	if costRaw == "" {
		t.Fatal("cost missing from Usage.JSON.ExtraFields")
	}
	got, err := strconv.ParseFloat(costRaw, 64)
	if err != nil {
		t.Fatalf("parse cost %q: %v", costRaw, err)
	}
	if want := 0.00000121; got != want {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

// TestCostAbsentFromNonFinalChunk confirms a content chunk with no usage
// object yields an empty Raw() (so costUSD stays 0), exercising the nil-map
// path the production loop hits on every non-final chunk.
func TestCostAbsentFromNonFinalChunk(t *testing.T) {
	const raw = `{"id":"gen-1","object":"chat.completion.chunk","created":1779227392,` +
		`"model":"qwen/qwen3-235b-a22b-07-25",` +
		`"choices":[{"index":0,"delta":{"content":"hi"}}]}`

	var chunk openai.ChatCompletionChunk
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		t.Fatalf("unmarshal chunk: %v", err)
	}
	if costRaw := chunk.Usage.JSON.ExtraFields["cost"].Raw(); costRaw != "" {
		t.Fatalf("expected no cost on non-final chunk, got %q", costRaw)
	}
}
