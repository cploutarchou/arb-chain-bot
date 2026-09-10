# Campaign 01M25GET9C8XVKD9358JT15BNC — post-remediation evidence run

- Recorded: 2026-09-10, 11:15–11:20 UTC, live Binance spot books
  (BTCUSDT, ETHUSDT, ETHBTC, BTCUSDC, ETHUSDC, USDCUSDT at 20 levels),
  5 m 15 s, one 2.0 MB segment (`recordings/01M25GET9C8XVKD9358JT15BNC/`).
- Replayed: the same day, on the remediated engine — exact breakpoint-aware
  sizer, gate-constrained objective, realized-vs-plan slippage, fees
  valued in the start asset, 10/10 bps venue defaults, 24 scenarios
  (baseline + fees +5/+10 bps, latency ×2/×4, 50 % fills, 50 %
  liquidity, adverse combo; seeds 1–3).
- Result: **0 qualified opportunities in every scenario.** No cycle
  executed, so no profitability statement can be made — and none is.
  This is consistent with every prior campaign (the 2026-08-27 run's
  best gross deviation was +4 bps against ≈40 bps of costs): the Binance
  triangular fee wall rejects the entire opportunity space at Regular
  tier during this window.
- What this run proves post-remediation: the measurement layer is honest
  end to end — a session recorded, registered and replayed through the
  current engine produced a decisive, correctly-shaped negative rather
  than a fabricated positive or a silent loss. The verdict of the FINAL
  PLATFORM REVIEW (`docs/audit/master-report.md`) remains
  **NOT READY FOR LIVE TRADING**; the positive-edge condition stays
  unmet by the market, not by missing engineering.
- Reproduce: record (RECORD mode) → `cmd/campaign -recording <id>
  -dir recordings/<id> -assets USDT -balance USDT=10000 -seeds 1,2,3
  -grid full -out docs/campaigns/<id>/campaign-<id>`.
