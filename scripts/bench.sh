#!/usr/bin/env bash
# T-044: run the §73 benchmark suite and print a comparison-ready
# summary. Compare against docs/benchmarks.md budgets by hand or with
# benchstat (go install golang.org/x/perf/cmd/benchstat@latest).
set -euo pipefail
cd "$(dirname "$0")/.."

PKGS=(
  ./internal/orderbook/
  ./internal/exchange/binance/
  ./internal/graph/
  ./internal/pricing/
  ./internal/opportunity/
  ./internal/scanner/
  ./internal/simulation/
  ./internal/realtime/
  ./internal/metrics/
)

go test -bench . -benchmem -benchtime="${BENCHTIME:-1s}" -run '^$' "${PKGS[@]}"
