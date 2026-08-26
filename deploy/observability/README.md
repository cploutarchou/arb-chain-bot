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
  data, scanner, risk/paper, API). Thresholds assume 15s scrapes.
- `grafana-dashboard.json` — starter dashboard: feed health, book
  states/age, scanner throughput, qualified edge, paper outcomes and
  PnL, API latency.

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
