# Observability

The `arbd` binary exports Prometheus metrics through the OpenTelemetry
SDK (SKILL.md §65 metric set; `internal/metrics`).

## Endpoints

- `ARB_METRICS_ADDR` unset (dev): `/metrics` is mounted on the API mux
  (`ARB_HTTP_ADDR`, default `:8080`). The endpoint carries no secrets.
- `ARB_METRICS_ADDR=:9109` (production posture): a dedicated listener
  serves `GET /metrics` on a private port; the public API mux does not
  expose it.

## Scrape config

```yaml
scrape_configs:
  - job_name: arbd
    scrape_interval: 15s
    static_configs:
      - targets: ["arbd:9109"]
```

## Files

- `prometheus-rules.yml` — alert rules grouped by subsystem (market
  data, scanner, risk/paper, slippage, API). Thresholds assume 15s
  scrapes.
- `platform-rules.yml` — platform/SRE rules: feeds, collectors,
  screener venues (offline/slow, O10), recorder, campaigns, database,
  backups.
- `grafana-dashboard.json` — starter dashboard: feed health, book
  states/age, scanner throughput, qualified edge, realized slippage
  p50/p95 (O7), paper outcomes and PnL, API latency.
- `grafana-dashboard-platform.json` — SRE dashboard: feed SLOs,
  rate limits, recorder, campaigns, screener venue health (O10), API,
  pod/Postgres/backup panels.

## Conventions

- Counters follow the SKILL names with the exporter's `_total` suffix
  (`market_messages_total`); `*_per_second` panels are PromQL `rate()`
  over the counters rather than separate series.
- Monetary series (`capital_*`, `paper_pnl`, `fees_total`) are
  display-only float conversions at the exposition boundary; canonical
  values stay decimal in the engine, API, and database.
- AI (`ai_requests_total`, `ai_failures_total`) and Telegram
  (`telegram_messages_total`, `telegram_errors_total`) series register
  together with their subsystems (T-036 / T-033); metrics are never
  exported for components a build does not run.
- Hot-path budget: per-frame and per-evaluation instrumentation is one
  atomic add; SDK reads happen at scrape time only. Benchmarks:
  `go test -bench . ./internal/metrics/`.

## Kubernetes additions (Phase 26)

- `servicemonitor.yaml` — Prometheus Operator scrape of the private
  metrics port + postgres-exporter.
- `platform-rules.yml` — SRE alerts (feed stale > 30s, collector 429s,
  recorder drops, campaign runs, migrations, replication lag, backups,
  restore drill, pod restarts) and SLO burn-rate rules. Series marked
  PENDING EXPORTER are listed at the top of the file.
- `render-rules.sh` — wraps both rule files as PrometheusRule objects.
- `grafana-dashboard-platform.json` — SRE dashboard (feed health,
  collector 429s, recorder, campaign runs, API latency, platform).
- `loki-values.yaml`, `promtail-values.yaml`, `tempo-values.yaml` —
  logs and traces stacks; audit stream retained 400d.

SLOs (30-day windows): API availability 99.9 %, API p99 < 500 ms,
feed freshness 99.5 % of minutes with max book age < 5 s. Burn-rate
alerts in `platform-rules.yml` group `arb-slo`.
