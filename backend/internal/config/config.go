// Package config loads environment variables and the models.yaml configuration.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// ModelEntry represents a single model available for debate agents.
type ModelEntry struct {
	ID   string `json:"id"   yaml:"id"`
	Name string `json:"name" yaml:"name"`
}

type modelsFile struct {
	Models []ModelEntry `yaml:"models"`
}

var (
	models       []ModelEntry
	project      string
	location     string
	useVertexAI  bool
	defaultModel string
	ttsEnabled bool
	ttsModel   string
	ttsAPIKey  string
)

// Pro preview TTS is noticeably less theatrical than Flash and follows
// style directives more faithfully (less accent/language bleed). Only
// served on the Gemini API backend — override via TTS_MODEL if running
// TTS on Vertex.
const defaultTTSModel = "gemini-2.5-pro-preview-tts"

// Load reads the .env file and models.yaml, populating package-level state.
func Load(envPath, modelsPath string) error {
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

	data, err := os.ReadFile(modelsPath)
	if err != nil {
		return fmt.Errorf("read models.yaml: %w", err)
	}
	var mf modelsFile
	if err := yaml.Unmarshal(data, &mf); err != nil {
		return fmt.Errorf("parse models.yaml: %w", err)
	}
	if len(mf.Models) == 0 {
		return fmt.Errorf("models.yaml contains no models")
	}
	models = mf.Models
	// Default model is always the first entry in models.yaml
	defaultModel = models[0].ID

	return nil
}

func Project() string      { return project }
func Location() string     { return location }
func UseVertexAI() bool    { return useVertexAI }
func DefaultModel() string { return defaultModel }
func Models() []ModelEntry { return models }
func TTSEnabled() bool     { return ttsEnabled }
func TTSModel() string     { return ttsModel }
func TTSAPIKey() string    { return ttsAPIKey }

// ValidModel returns true if modelID matches a configured model.
func ValidModel(modelID string) bool {
	for _, m := range models {
		if m.ID == modelID {
			return true
		}
	}
	return false
}
