# Monitoring

OTel metrics from `logos-backend` → Grafana Cloud (Prometheus + Grafana). Logs remain in Cloud Logging for high-cardinality SQL queries.

## Architecture

```
Cloud Run (logos-backend)
  ├── slog → stdout → Cloud Logging   ◄── "top N users", trace correlation, debugging
  └── OTel SDK → OTLP/HTTP push ──▶ Grafana Cloud Prometheus
                                             │
                                             ▼
                                       Grafana dashboards
```

Metrics are emitted from [../backend/internal/agent/instrumented.go](../backend/internal/agent/instrumented.go), the single wrapper every agent LLM call passes through.

## Instruments

| Name (OTel) | Prometheus name | Type | Attributes |
|---|---|---|---|
| `llm.input.tokens` | `llm_input_tokens_total` | Counter (`{token}`) | `model`, `role` |
| `llm.output.tokens` | `llm_output_tokens_total` | Counter (`{token}`) | `model`, `role` |
| `llm.cost.usd` | `llm_cost_usd_total` | Counter (`{USD}`) | `model`, `role` |
| `llm.ttft` | `llm_ttft_seconds_bucket` | Histogram (`s`) | `model`, `role` |
| `llm.errors` | `llm_errors_total` | Counter (`{error}`) | `model`, `role`, `error_class` |

`llm.cost.usd` is the provider-billed cost (OpenRouter `usage.cost`), summed straight from the LLM call — no price table. Cost panels read `rate`/`increase` over `llm_cost_usd_total`.

OTel → Prometheus naming: dots become underscores, counters get `_total`, histograms expose `_bucket` / `_sum` / `_count`. SDK does this automatically.

## One-time setup

### 1. Grafana Cloud OTLP credentials

In the Grafana Cloud portal → **My Account** → **Details** → **OpenTelemetry**, click *Configure*. You need two values: the **OTLP endpoint** URL and the **Authorization header** (Basic auth with `instance_id:token` base64-encoded — the portal pre-builds the full header string for you).

### 2. Configure Cloud Run

```bash
gcloud run services update logos-backend \
  --project=logos-494817 --region=europe-west1 \
  --update-env-vars="\
OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-<region>.grafana.net/otlp,\
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic%20<base64-from-portal>"
```

The `Basic%20` (URL-encoded space) is mandatory — gcloud's env-var parser splits on raw spaces.

**Also extend the SIGTERM grace period** (the default 10s is too short — LLM calls often take 20s+, and we need budget for both the http shutdown and the metrics flush):

```bash
gcloud run services update logos-backend \
  --project=logos-494817 --region=europe-west1 \
  --service-min-instances=0 \
  --update-annotations=run.googleapis.com/termination-grace-period=60s
```

(Or set `terminationGracePeriodSeconds` via the service YAML if you manage Cloud Run as code.) `main.go` budgets 30s for http shutdown + 5s for telemetry flush within that 60s window.

If `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, `telemetry.Init` logs `telemetry disabled` and the SDK is a no-op. Useful for local dev.

### 3. Import the dashboard

Grafana Cloud → **Dashboards** → **New** → **Import**, paste `dashboards/llm_tokens.json`. On the import screen, pick your Prometheus datasource for the `DS_PROMETHEUS` prompt.

Authored in the **classic dashboard schema (V1)**. V2 ([GA Apr 2026](https://grafana.com/whats-new/2026-04-08-dynamic-dashboards-is-now-generally-available/)) is the runtime default in Grafana Cloud, but the UI import path doesn't accept V2 JSON directly yet — V2 ingestion currently lives in the grafana-operator/CRD path. V1 imports cleanly and Grafana auto-migrates it to V2 internally on first open.

**Verify metric names after the first data lands.** Grafana Cloud's OTLP-to-Prometheus translator may add a namespace prefix or transform names slightly. Open **Explore** → Prometheus → metrics browser and search `llm_` to confirm what's actually stored. If you see e.g. `logos_llm_input_tokens_total` (with a service prefix), update the panel `expr` fields to match. The instrument names in [../backend/internal/agent/metrics.go](../backend/internal/agent/metrics.go) are `llm.input.tokens`, `llm.output.tokens`, `llm.cost.usd`, `llm.ttft`, `llm.errors`; everything else is the translator's choice.

## Verifying it works

1. **Cold smoke (no Grafana account):** local `go run` with `OTEL_EXPORTER_OTLP_ENDPOINT=""` should print `telemetry disabled` and run normally.
2. **Local with debug collector:** run an OTel collector in Docker pointed at stdout, set `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`, trigger one debate turn, watch counter increments print.
3. **Deployed:** open Grafana Cloud → **Explore** → Prometheus → run `llm_input_tokens_total`. First values appear ~30 s after the first LLM call (the SDK push interval).
4. **Dashboard:** open *Logos LLM*. The Overview row shows TTFT, errors, and cost stats; the **LLM** and **Cost** rows expand to per-`(model, role)` detail.

## "Top N users by tokens / cost" (logs, not metrics)

Per-user attribution is a cardinality-explosion antipattern in Prometheus, perfect for Log Analytics. Cost lives on the `llm_usage` log line as `cost_usd` (provider-billed), so the same query yields spend per user. In Cloud Console → **Logging** → **Log Analytics**:

```sql
SELECT
  json_payload.uid AS user,
  SUM(CAST(json_payload.input_tokens AS INT64) + CAST(json_payload.output_tokens AS INT64)) AS total_tokens,
  SUM(CAST(json_payload.cost_usd AS FLOAT64)) AS total_cost_usd
FROM `logos-494817.global._Default._AllLogs`
WHERE json_payload.event = 'llm_usage'
  AND timestamp >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 1 DAY)
GROUP BY user
ORDER BY total_cost_usd DESC
LIMIT 5;
```

Adjust the interval (`1 DAY` → `7 DAY` for weekly, etc.).

## Deferred

- TTS instrumentation (`tts.characters` counter) — `tts_usage` log events aren't emitted yet.
- Alerts (cost threshold, error spike, TTFT regression) — wait until baseline is known.
- Dashboard-as-code via terraform-provider-grafana — V2 support is still landing ([issue #2543](https://github.com/grafana/terraform-provider-grafana/issues/2543)). Import via UI for now.
- Traces (OTLP traces export) — same SDK pattern, separate slice.
