// Package config loads environment variables for the debate system.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

var (
	ttsEnabled      bool
	ttsModel        string
	ttsBucket       string // GCS bucket for persisted narrations (STORAGE_BACKEND=firestore)
	llmAPIKey       string
	llmBaseURL      string
	gcpProject      string // runtime GCP project: Firestore + Cloud Logging trace links
	firebaseProject string // project that issues the Firebase ID tokens (auth verifier)
	authAllowlist   string // comma-separated emails/domains permitted past auth
	corsOrigins     string // comma-separated browser origins permitted by CORS

	quotaLimitUSD float64 // per-user lifetime USD budget; mandatory (ADR 0009)

	ttsUSDPer1MAudioTokens float64 // synthesis price (ADR 0013); Gemini bills 25 audio tokens/s
	ttsUSDPerGBEgress      float64 // egress price for serving narration bytes (ADR 0013)
)

// DefaultLLMBaseURL is the default OpenAI-compatible endpoint when
// LLM_BASE_URL is unset. OpenRouter is the recommended provider
// because of its prepaid hard spend cap (see ADR 0007), but the
// implementation works against any OpenAI-compatible API (OpenAI,
// Together AI, Groq, Azure OpenAI, vLLM, Ollama, etc.).
const DefaultLLMBaseURL = "https://openrouter.ai/api/v1"

// Default TTS model in OpenRouter provider/model form. Resolves to
// the same Gemini weights we used pre-migration (per ADR 0002 rev).
const defaultTTSModel = "google/gemini-3.1-flash-tts-preview"

// Load reads the .env file and environment variables, populating package-level state.
func Load(envPath string) error {
	_ = godotenv.Load(envPath)

	llmAPIKey = os.Getenv("LLM_API_KEY")
	llmBaseURL = os.Getenv("LLM_BASE_URL")
	if llmBaseURL == "" {
		llmBaseURL = DefaultLLMBaseURL
	}
	gcpProject = os.Getenv("GOOGLE_CLOUD_PROJECT")
	firebaseProject = os.Getenv("FIREBASE_PROJECT")
	authAllowlist = os.Getenv("AUTH_ALLOWLIST")
	corsOrigins = os.Getenv("CORS_ALLOWED_ORIGINS")
	ttsEnabled = strings.EqualFold(os.Getenv("TTS_ENABLED"), "TRUE")
	ttsModel = os.Getenv("TTS_MODEL")
	if ttsModel == "" {
		ttsModel = defaultTTSModel
	}
	ttsBucket = os.Getenv("TTS_BUCKET")
	var err error
	if quotaLimitUSD, err = parseNonNegFloat("QUOTA_USD_LIMIT"); err != nil {
		return err
	}
	if ttsUSDPer1MAudioTokens, err = parseNonNegFloat("TTS_USD_PER_1M_AUDIO_TOKENS"); err != nil {
		return err
	}
	if ttsUSDPerGBEgress, err = parseNonNegFloat("TTS_USD_PER_GB_EGRESS"); err != nil {
		return err
	}

	return nil
}

// parseNonNegFloat reads a required non-negative float env var, erroring when
// unset, unparseable, or negative.
func parseNonNegFloat(name string) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, fmt.Errorf("config: %s is required", name)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("config: %s must be a non-negative number", name)
	}
	return v, nil
}

func TTSEnabled() bool { return ttsEnabled }
func TTSModel() string { return ttsModel }

// TTSBucket returns the GCS bucket name for persisted narrations (TTS_BUCKET).
// Empty unless audio persistence is configured; required when
// STORAGE_BACKEND=firestore and TTS is enabled (see main.buildNarrationStore).
func TTSBucket() string { return ttsBucket }

// LLMAPIKey returns the API key for the OpenAI-compatible LLM
// provider configured via LLM_BASE_URL (OpenRouter by default).
func LLMAPIKey() string { return llmAPIKey }

// LLMBaseURL returns the OpenAI-compatible base URL. Defaults to
// OpenRouter; override via LLM_BASE_URL env var to target any other
// OpenAI-compatible endpoint.
func LLMBaseURL() string { return llmBaseURL }

// Project returns GOOGLE_CLOUD_PROJECT: the GCP project this backend runs in.
// Used for Firestore storage and for Cloud Logging trace-clickthrough links.
func Project() string { return gcpProject }

// FirebaseProject returns the project that issues the Firebase ID tokens the
// auth verifier validates (FIREBASE_PROJECT). This is the project the frontend
// authenticates against, which can differ from the runtime Project() when
// Hosting/Auth live in a separate (e.g. free-tier) GCP project.
func FirebaseProject() string { return firebaseProject }

// AuthAllowlist returns the raw AUTH_ALLOWLIST spec: a comma-separated list of
// emails and/or domains permitted past auth ("*" allows all). Empty denies
// everyone (fail-closed). Parse it with auth.ParseAllowlist.
func AuthAllowlist() string { return authAllowlist }

// CORSAllowedOrigins returns the raw CORS_ALLOWED_ORIGINS spec: a comma-separated
// list of browser origins permitted by CORS. Empty falls back to the local dev origin.
func CORSAllowedOrigins() string { return corsOrigins }

// QuotaLimitUSD returns the required per-user lifetime budget in USD
// (QUOTA_USD_LIMIT).
func QuotaLimitUSD() float64 { return quotaLimitUSD }

// USDPer1MAudioTokens returns the required TTS synthesis price per 1,000,000
// audio tokens (TTS_USD_PER_1M_AUDIO_TOKENS), used to meter narration synthesis
// from the byte count (ADR 0013).
func USDPer1MAudioTokens() float64 { return ttsUSDPer1MAudioTokens }

// USDPerGBEgress returns the required price per GB for serving narration bytes
// (TTS_USD_PER_GB_EGRESS), used to meter egress on every narration call (ADR 0013).
func USDPerGBEgress() float64 { return ttsUSDPerGBEgress }
