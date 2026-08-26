# Resource: Observability

Authoritative documents: `docs/architecture.md` §14, SKILL.md §65–§66.
Implementation home: `internal/metrics`, logging setup in `internal/app`,
dashboards/alert rules in `deploy/observability/`.

Working rules:
- OpenTelemetry SDK, Prometheus exporter at `/metrics`. Metric names per
  SKILL.md §65 (market_messages_total, orderbook_age_ms,
  orderbook_sequence_errors, triangles_evaluated_total,
  opportunities_{detected,qualified,rejected}_total, net_edge_bps,
  paper_cycles_*, paper_pnl, fees_total, slippage_bps, capital_*,
  exchange_reconnects_total, ai_*, telegram_*, api_request_duration,
  websocket_clients, circuit_breaker_state, …). Labels bounded (exchange,
  market, triangle_id only where cardinality is safe; no unbounded IDs).
- Hot-path instrumentation: atomic counters/gauges only; histograms fed
  off-path; no network calls inline; overhead proven by benchmark.
- Logging: slog JSON with timestamp, level, service, event,
  correlation_id, exchange, triangle_id, opportunity_id, cycle_id,
  order_id. Never secrets. Market ticks never at INFO; high-volume paths
  sampled or DEBUG.
- Health/readiness endpoints; System Health API aggregates component
  states, queue depths, goroutines, GC, DB pool, per-feed health, clock
  quality.
- Grafana-compatible dashboards + alert rules versioned in deploy/;
  alert routing through NotificationService severities.
