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
)

// Load reads the .env file and models.yaml, populating package-level state.
func Load(envPath, modelsPath string) error {
	// Load .env (ignore error if file doesn't exist)
	_ = godotenv.Load(envPath)

	project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	location = os.Getenv("GOOGLE_CLOUD_LOCATION")
	useVertexAI = strings.EqualFold(os.Getenv("GOOGLE_GENAI_USE_VERTEXAI"), "TRUE")

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

// ValidModel returns true if modelID matches a configured model.
func ValidModel(modelID string) bool {
	for _, m := range models {
		if m.ID == modelID {
			return true
		}
	}
	return false
}
