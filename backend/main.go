package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/philosopher"
	"github.com/h-hmz/logos/internal/server"
)

func main() {
	// Resolve data directory — defaults to the directory containing the binary.
	// When using "go run", the binary is in a tmp dir so fall back to ".".
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		exe, _ := os.Executable()
		dataDir = filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dataDir, "models.yaml")); err != nil {
			dataDir = "."
		}
	}

	envPath := filepath.Join(dataDir, ".env")
	modelsPath := filepath.Join(dataDir, "models.yaml")
	philDir := filepath.Join(dataDir, "agents", "philosophers")

	if err := config.Load(envPath, modelsPath); err != nil {
		log.Fatalf("config: %v", err)
	}

	philosophers, err := philosopher.LoadAll(philDir)
	if err != nil {
		log.Fatalf("philosophers: %v", err)
	}
	log.Printf("Loaded %d philosophers", len(philosophers))

	router := server.New(philosophers, config.Models())

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	log.Printf("Logos Arena listening on :%s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatalf("server: %v", err)
	}
}
