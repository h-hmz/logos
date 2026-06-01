// Package config loads environment variables for the debate system.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

var (
	defaultModel    string
	ttsEnabled      bool
	ttsModel        string
	llmAPIKey       string
	llmBaseURL      string
	gcpProject      string // runtime GCP project: Firestore + Cloud Logging trace links
	firebaseProject string // project that issues the Firebase ID tokens (auth verifier)
	authAllowlist   string // comma-separated emails/domains permitted past auth
	corsOrigins     string // comma-separated browser origins permitted by CORS

	quotaEnabled  bool    // gate per-user generation quotas (ADR 0009)
	quotaLimitUSD float64 // lifetime USD budget per user
)

// DefaultQuotaLimitUSD is the per-user lifetime budget when QUOTA_USD_LIMIT is
// unset. See ADR 0009.
const DefaultQuotaLimitUSD = 1.0

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
	defaultModel = os.Getenv("MODEL")
	if defaultModel == "" {
		return errors.New("config: MODEL env var is required")
	}

	quotaEnabled = strings.EqualFold(os.Getenv("QUOTA_ENABLED"), "TRUE")
	quotaLimitUSD = DefaultQuotaLimitUSD
	if raw := strings.TrimSpace(os.Getenv("QUOTA_USD_LIMIT")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 {
			return errors.New("config: QUOTA_USD_LIMIT must be a non-negative number")
		}
		quotaLimitUSD = v
	}

	return nil
}

func DefaultModel() string { return defaultModel }
func TTSEnabled() bool     { return ttsEnabled }
func TTSModel() string     { return ttsModel }

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

// QuotaEnabled reports whether per-user generation quotas are enforced
// (QUOTA_ENABLED=true). Defaults to false so local dev and tests run unmetered.
func QuotaEnabled() bool { return quotaEnabled }

// QuotaLimitUSD returns the per-user lifetime budget in USD (QUOTA_USD_LIMIT),
// defaulting to DefaultQuotaLimitUSD.
func QuotaLimitUSD() float64 { return quotaLimitUSD }
