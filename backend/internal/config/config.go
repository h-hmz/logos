// Package config loads environment variables for the debate system.
package config

import (
	"errors"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

var (
	defaultModel string
	ttsEnabled   bool
	ttsModel     string
	llmAPIKey     string
	llmBaseURL    string
	gcpProject    string // for Firebase auth + Firestore (not LLM/TTS)
	authAllowlist string // comma-separated emails/domains permitted past auth
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
	authAllowlist = os.Getenv("AUTH_ALLOWLIST")
	ttsEnabled = strings.EqualFold(os.Getenv("TTS_ENABLED"), "TRUE")
	ttsModel = os.Getenv("TTS_MODEL")
	if ttsModel == "" {
		ttsModel = defaultTTSModel
	}
	defaultModel = os.Getenv("MODEL")
	if defaultModel == "" {
		return errors.New("config: MODEL env var is required")
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

// Project returns GOOGLE_CLOUD_PROJECT for Firebase auth + Firestore.
// LLM and TTS no longer go through GCP; this is kept only for those
// two services.
func Project() string { return gcpProject }

// AuthAllowlist returns the raw AUTH_ALLOWLIST spec: a comma-separated list of
// emails and/or domains permitted past auth ("*" allows all). Empty denies
// everyone (fail-closed). Parse it with auth.ParseAllowlist.
func AuthAllowlist() string { return authAllowlist }
