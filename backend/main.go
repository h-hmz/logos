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

	"github.com/h-hmz/logos/internal/auth"
	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/debate"
	"github.com/h-hmz/logos/internal/philosopher"
	"github.com/h-hmz/logos/internal/quota"
	"github.com/h-hmz/logos/internal/server"
	"github.com/h-hmz/logos/internal/telemetry"
	"github.com/h-hmz/logos/internal/tts"
)

func main() {
	if err := run(context.Background(), os.Stdout, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %s\n", err)
		os.Exit(1)
	}
}

// contextHandler injects per-request fields carried on ctx (request_id, uid,
// trace_id, session_id) into every record so callers can keep using the
// package-level slog.*Context helpers instead of threading a scoped logger
// through every layer.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := server.RequestIDFromContext(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	if uid := server.UIDFromContext(ctx); uid != "" {
		r.AddAttrs(slog.String("uid", uid))
	}
	if tid := server.TraceIDFromContext(ctx); tid != "" {
		r.AddAttrs(slog.String("trace_id", tid))
		// Cloud Logging clickthrough requires the qualified form
		// "projects/<PROJECT>/traces/<TRACE_ID>" under the magic key
		// logging.googleapis.com/trace; the plain trace_id alone won't
		// link from Logs Explorer to the Trace UI.
		if proj := config.Project(); proj != "" {
			r.AddAttrs(slog.String("logging.googleapis.com/trace", "projects/"+proj+"/traces/"+tid))
			if sid := server.SpanIDFromContext(ctx); sid != "" {
				r.AddAttrs(slog.String("logging.googleapis.com/spanId", sid))
			}
		}
	}
	if sid := server.SessionIDFromContext(ctx); sid != "" {
		r.AddAttrs(slog.String("session_id", sid))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

// gcpReplaceAttr rewrites slog's built-in keys to the special JSON fields Cloud
// Logging recognizes: "level" → "severity" and "msg" → "message". slog renders
// the warn level as "WARN", but the Cloud Logging LogSeverity enum spells it
// "WARNING". Only top-level attrs are touched so grouped attrs (e.g. the
// httpRequest group) pass through untouched.
func gcpReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) != 0 {
		return a
	}
	switch a.Key {
	case slog.LevelKey:
		a.Key = "severity"
		if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == slog.LevelWarn {
			a.Value = slog.StringValue("WARNING")
		}
	case slog.MessageKey:
		a.Key = "message"
	}
	return a
}

func run(ctx context.Context, w io.Writer, _ []string) error {
	opts := &slog.HandlerOptions{ReplaceAttr: gcpReplaceAttr}
	slog.SetDefault(slog.New(contextHandler{slog.NewJSONHandler(w, opts)}))

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Resolve data directory — defaults to the directory containing the binary.
	// When using "go run", the binary is in a tmp dir so fall back to ".".
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		exe, _ := os.Executable()
		dataDir = filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dataDir, "agents")); err != nil {
			dataDir = "."
		}
	}

	envPath := filepath.Join(dataDir, ".env")
	philDir := filepath.Join(dataDir, "agents", "philosophers")

	if err := config.Load(envPath); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	shutdownTelemetry, err := telemetry.Init(ctx)
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}

	philosophers, err := philosopher.LoadAllPhilosophers(philDir)
	if err != nil {
		return fmt.Errorf("philosophers: %w", err)
	}
	slog.Info("Loaded philosophers", "count", len(philosophers))

	repo, err := buildRepo(ctx)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	narrations, err := buildNarrationStore(ctx)
	if err != nil {
		return fmt.Errorf("narration storage: %w", err)
	}
	verifier, err := buildVerifier(ctx)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	allowlist := auth.ParseAllowlist(config.AuthAllowlist())
	devNoAuth := strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_BACKEND")), "none")
	switch {
	case devNoAuth && allowlist.Empty():
		// AUTH_BACKEND=none disables real auth for local dev — the noop verifier
		// accepts any token. A fail-closed allowlist would just lock dev out for
		// no security benefit, so allow all here. An explicit AUTH_ALLOWLIST
		// still takes precedence.
		allowlist = auth.ParseAllowlist("*")
		slog.Warn("auth: allowlist bypassed (AUTH_BACKEND=none) — all callers allowed")
	case allowlist.Open():
		slog.Warn("auth: allowlist open (AUTH_ALLOWLIST=*) — every verified caller is allowed")
	case allowlist.Empty():
		slog.Warn("auth: allowlist empty — all callers will be denied; set AUTH_ALLOWLIST to emails/domains")
	default:
		slog.Info("auth: allowlist configured")
	}
	corsOrigins := splitCSV(config.CORSAllowedOrigins())
	if len(corsOrigins) == 0 {
		slog.Warn("cors: no CORS_ALLOWED_ORIGINS set — only the local Vite dev origin is allowed")
	} else {
		slog.Info("cors: allowed origins configured", "origins", corsOrigins)
	}
	quotaDeps, err := buildQuota(ctx)
	if err != nil {
		return fmt.Errorf("quota: %w", err)
	}
	router := server.New(philosophers, repo, narrations, verifier, allowlist, corsOrigins, quotaDeps)

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

		// Requires Cloud Run terminationGracePeriodSeconds=60
		// (default 10s is too tight for LLM calls).
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Warn("http server shutdown error", "err", err)
		}

		flushCtx, flushCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer flushCancel()
		if err := shutdownTelemetry(flushCtx); err != nil {
			slog.Warn("telemetry shutdown error", "err", err)
		}
	}

	return nil
}

// buildVerifier selects the auth.Verifier implementation from AUTH_BACKEND.
// Defaults to firebase in production; set AUTH_BACKEND=none for local dev to
// skip the Firebase Admin SDK init. The noop verifier still rejects missing
// tokens, so client code exercises the auth header path.
func buildVerifier(ctx context.Context) (auth.Verifier, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("AUTH_BACKEND")))
	switch backend {
	case "none":
		slog.Warn("auth: noop verifier (AUTH_BACKEND=none) — do not use in production")
		return auth.NewNoopVerifier(), nil
	case "", "firebase":
		firebaseProjectID := config.FirebaseProject()
		if firebaseProjectID == "" {
			return nil, fmt.Errorf("AUTH_BACKEND=firebase requires FIREBASE_PROJECT")
		}
		v, err := auth.NewFirebaseVerifier(ctx, firebaseProjectID)
		if err != nil {
			return nil, err
		}
		slog.Info("auth: firebase", "project", firebaseProjectID)
		return v, nil
	default:
		return nil, fmt.Errorf("unknown AUTH_BACKEND=%q (want firebase|none)", backend)
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

// buildNarrationStore selects the tts.NarrationStore implementation. It is gated
// on TTS_ENABLED (returns nil when TTS is off — the /tts handler 503s before the
// store is touched) and otherwise follows the same STORAGE_BACKEND as debates:
// in-memory for dev, GCS for Cloud Run. STORAGE_BACKEND=firestore requires
// TTS_BUCKET. See ADR 0012.
func buildNarrationStore(ctx context.Context) (tts.NarrationStore, error) {
	if !config.TTSEnabled() {
		slog.Warn("narration storage: disabled (TTS_ENABLED not set)")
		return nil, nil
	}
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_BACKEND")))
	switch backend {
	case "", "memory":
		slog.Info("narration storage: in-memory")
		return tts.NewMemoryNarrationStore(), nil
	case "firestore":
		bucket := config.TTSBucket()
		if bucket == "" {
			return nil, fmt.Errorf("STORAGE_BACKEND=firestore with TTS_ENABLED requires TTS_BUCKET")
		}
		store, err := tts.NewGCSNarrationStore(ctx, bucket)
		if err != nil {
			return nil, err
		}
		slog.Info("narration storage: gcs", "bucket", bucket)
		return store, nil
	default:
		return nil, fmt.Errorf("unknown STORAGE_BACKEND=%q (want memory|firestore)", backend)
	}
}

// buildQuota constructs the per-user quota dependencies.The spend store follows the same STORAGE_BACKEND as
// debates: in-memory for dev, Firestore for Cloud Run.
func buildQuota(ctx context.Context) (*server.QuotaDeps, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_BACKEND")))
	var store quota.Store
	switch backend {
	case "", "memory":
		store = quota.NewMemoryStore()
		slog.Info("quota storage: in-memory")
	case "firestore":
		projectID := config.Project()
		if projectID == "" {
			return nil, fmt.Errorf("STORAGE_BACKEND=firestore requires GOOGLE_CLOUD_PROJECT")
		}
		database := os.Getenv("FIRESTORE_DATABASE")
		collection := os.Getenv("QUOTA_COLLECTION")
		if collection == "" {
			collection = "quotas"
		}
		fs, err := quota.NewFirestoreStore(ctx, projectID, database, collection)
		if err != nil {
			return nil, err
		}
		dbLabel := database
		if dbLabel == "" {
			dbLabel = "(default)"
		}
		slog.Info("quota storage: firestore", "project", projectID, "database", dbLabel, "collection", collection)
		store = fs
	default:
		return nil, fmt.Errorf("unknown STORAGE_BACKEND=%q (want memory|firestore)", backend)
	}

	slog.Info("quota: enabled", "limit_usd", config.QuotaLimitUSD())
	return &server.QuotaDeps{
		Store:           store,
		DefaultLimitUSD: config.QuotaLimitUSD(),
		InFlight:        quota.NewInFlight(),
	}, nil
}

// splitCSV splits a comma-separated env value into trimmed, non-empty entries.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
