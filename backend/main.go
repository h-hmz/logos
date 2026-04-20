package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/philosopher"
	"github.com/h-hmz/logos/internal/server"
)

func main() {
	if err := run(context.Background(), os.Stdout, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %s\n", err)
		os.Exit(1)
	}
}

// contextHandler injects the request_id carried on ctx into every record, so
// callers can keep using the package-level slog.*Context helpers instead of
// threading a scoped logger through every layer.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := server.RequestIDFromContext(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

func run(ctx context.Context, w io.Writer, args []string) error {
	slog.SetDefault(slog.New(contextHandler{slog.NewJSONHandler(w, nil)}))

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

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
		return fmt.Errorf("config: %w", err)
	}

	philosophers, err := philosopher.LoadAll(philDir)
	if err != nil {
		return fmt.Errorf("philosophers: %w", err)
	}
	slog.Info("Loaded philosophers", "count", len(philosophers))

	repo, err := buildRepo(ctx)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	router := server.New(philosophers, config.Models(), repo)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	srvErr := make(chan error, 1)

	go func() {
		slog.Info("Logos listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- err
		}
	}()

	select {
	case err := <-srvErr:
		return fmt.Errorf("server startup failed: %w", err)
	case <-ctx.Done():
		slog.Info("Interrupt signal received, initiating graceful shutdown...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("error during server shutdown: %w", err)
		}
	}

	return nil
}

// buildRepo selects the debate.Repository implementation from STORAGE_BACKEND.
// Defaults to in-memory. STORAGE_BACKEND=firestore requires GOOGLE_CLOUD_PROJECT
// and honors optional FIRESTORE_DATABASE (default "(default)") and
// FIRESTORE_COLLECTION (default "debates").
func buildRepo(ctx context.Context) (debate.Repository, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_BACKEND")))
	switch backend {
	case "", "memory":
		slog.Info("storage: in-memory")
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
		slog.Info("storage: firestore", "project", projectID, "database", dbLabel, "collection", collection)
		return repo, nil
	default:
		return nil, fmt.Errorf("unknown STORAGE_BACKEND=%q (want memory|firestore)", backend)
	}
}
