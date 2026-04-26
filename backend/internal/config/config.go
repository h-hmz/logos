// Package config loads environment variables for the debate system.
package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

var (
	project      string
	location     string
	useVertexAI  bool
	defaultModel string
	ttsEnabled   bool
	ttsModel     string
	ttsAPIKey    string
)

// Pro preview TTS is noticeably less theatrical than Flash and follows
// style directives more faithfully (less accent/language bleed). Only
// served on the Gemini API backend — override via TTS_MODEL if running
// TTS on Vertex.
const defaultTTSModel = "gemini-2.5-pro-preview-tts"
const fallbackModel = "gemini-3-flash-preview"

// Load reads the .env file and environment variables, populating package-level state.
func Load(envPath string) error {
	// Load .env (ignore error if file doesn't exist)
	_ = godotenv.Load(envPath)

	project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	location = os.Getenv("GOOGLE_CLOUD_LOCATION")
	useVertexAI = strings.EqualFold(os.Getenv("GOOGLE_GENAI_USE_VERTEXAI"), "TRUE")
	ttsEnabled = strings.EqualFold(os.Getenv("TTS_ENABLED"), "TRUE")
	ttsModel = os.Getenv("TTS_MODEL")
	if ttsModel == "" {
		ttsModel = defaultTTSModel
	}
	// Resolution order: explicit TTS override, then the app-wide Google key.
	ttsAPIKey = os.Getenv("TTS_API_KEY")
	if ttsAPIKey == "" {
		ttsAPIKey = os.Getenv("GOOGLE_API_KEY")
	}
	defaultModel = os.Getenv("MODEL")
	if defaultModel == "" {
		defaultModel = fallbackModel
	}

	return nil
}

func Project() string      { return project }
func Location() string     { return location }
func UseVertexAI() bool    { return useVertexAI }
func DefaultModel() string { return defaultModel }
func TTSEnabled() bool     { return ttsEnabled }
func TTSModel() string     { return ttsModel }
func TTSAPIKey() string    { return ttsAPIKey }
