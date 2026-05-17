// Package telemetry initializes the global OpenTelemetry meter provider
// for OTLP/HTTP push to a Prometheus-compatible backend (e.g. Grafana Cloud).
//
// Configuration is via the standard OTel env vars:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT   e.g. https://otlp-gateway-prod-eu-west-3.grafana.net/otlp
//	OTEL_EXPORTER_OTLP_HEADERS    e.g. Authorization=Basic%20<base64>
//
// If OTEL_EXPORTER_OTLP_ENDPOINT is empty, Init returns a no-op shutdown and
// logs "telemetry disabled" — lets local dev run without an OTel backend.
package telemetry

import (
	"context"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// pushInterval is how often the SDK flushes accumulated counter/histogram
// deltas to the OTLP endpoint. Short enough that dashboards feel live,
// long enough to keep the egress volume trivial.
const pushInterval = 30 * time.Second

// Init wires the global OTel MeterProvider. The returned shutdown func must be
// called on process exit to flush any buffered metrics — main.go runs it
// inside the SIGTERM-graceful-shutdown branch with its own short timeout.
// Cloud Run service is expected to be configured with
// terminationGracePeriodSeconds=60 so http shutdown + this flush both fit.
func Init(ctx context.Context) (func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		slog.Info("telemetry disabled (OTEL_EXPORTER_OTLP_ENDPOINT not set)")
		return func(context.Context) error { return nil }, nil
	}

	// Manual resource only: skipping the GCP resource detector to avoid the
	// GCE-metadata-server roundtrip at startup. service.version is populated
	// from Cloud Run's automatically-injected K_REVISION env var.
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("logos-backend"),
		semconv.ServiceVersion(os.Getenv("K_REVISION")),
	))
	if err != nil {
		return nil, err
	}

	// Exporter reads endpoint + headers from env directly (no programmatic
	// override needed). otlpmetrichttp honors OTEL_EXPORTER_OTLP_ENDPOINT
	// and OTEL_EXPORTER_OTLP_HEADERS.
	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(pushInterval),
		)),
	)
	otel.SetMeterProvider(provider)

	slog.Info("telemetry initialized", "endpoint", endpoint, "push_interval", pushInterval)
	return provider.Shutdown, nil
}
