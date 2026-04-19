package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
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

	repo, err := buildRepo(context.Background())
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	router := server.New(philosophers, config.Models(), repo)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	log.Printf("Logos listening on :%s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// buildRepo selects the debate.Repository implementation from STORAGE_BACKEND.
// Defaults to in-memory. STORAGE_BACKEND=firestore requires GOOGLE_CLOUD_PROJECT
// and honors optional FIRESTORE_DATABASE (default "(default)") and
// FIRESTORE_COLLECTION (default "debates").
func buildRepo(ctx context.Context) (debate.Repository, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_BACKEND")))
	switch backend {
	case "", "memory":
		log.Printf("storage: in-memory")
		return debate.NewMemoryRepo(), nil
	case "firestore":
		projectID := config.Project()
		if projectID == "" {
			return nil, fmt.Errorf("STORAGE_BACKEND=firestore requires GOOGLE_CLOUD_PROJECT")
		}
		database := os.Getenv("FIRESTORE_DATABASE")
		collection := os.Getenv("FIRESTORE_COLLECTION")
		if collection == "" {
			collection = "debates"
		}
		repo, err := debate.NewFirestoreRepo(ctx, projectID, database, collection)
		if err != nil {
			return nil, err
		}
		dbLabel := database
		if dbLabel == "" {
			dbLabel = "(default)"
		}
		log.Printf("storage: firestore project=%s database=%s collection=%s", projectID, dbLabel, collection)
		return repo, nil
	default:
		return nil, fmt.Errorf("unknown STORAGE_BACKEND=%q (want memory|firestore)", backend)
	}
}
