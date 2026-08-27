# Profitability validation campaign (§80)

Recording: `01M0ZPK16CXTR91MMJQ60HC2K3` · generated 2026-08-27T01:36:02Z

Paper simulation over recorded market data — not a live-trading result. Fills are simulated with seeded latency against replayed books that continue moving during the simulated waits; stress axes per §80: higher fees, higher latency, worse fills, lower liquidity.

| scenario | seeds | cycles | all-filled | fail-rate | mean gross bps | mean net bps | mean slippage bps | mean leg latency | fees paid | net PnL (USDT) | turnover | max drawdown |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| baseline | 3 | 0 | 0 | n/a | n/a | n/a | n/a | n/a | 0 | 0 | 0 | 0.0000 |
| fees+5bps | 3 | 0 | 0 | n/a | n/a | n/a | n/a | n/a | 0 | 0 | 0 | 0.0000 |
| fees+10bps | 3 | 0 | 0 | n/a | n/a | n/a | n/a | n/a | 0 | 0 | 0 | 0.0000 |
| latency-x2 | 3 | 0 | 0 | n/a | n/a | n/a | n/a | n/a | 0 | 0 | 0 | 0.0000 |
| latency-x4 | 3 | 0 | 0 | n/a | n/a | n/a | n/a | n/a | 0 | 0 | 0 | 0.0000 |
| fills-50pct | 3 | 0 | 0 | n/a | n/a | n/a | n/a | n/a | 0 | 0 | 0 | 0.0000 |
| liquidity-50pct | 3 | 0 | 0 | n/a | n/a | n/a | n/a | n/a | 0 | 0 | 0 | 0.0000 |
| adverse-combo | 3 | 0 | 0 | n/a | n/a | n/a | n/a | n/a | 0 | 0 | 0 | 0.0000 |

Measures: gross/net bps are the planner's per-cycle economics (fees in; buffers out for net); slippage is realized-vs-plan in bps of input, only for cycles that returned to the start asset (positive = worse); fail-rate counts executed cycles that did not settle ALL_FILLED; turnover is start-asset input deployed; capital efficiency = net PnL ÷ turnover.

## Verdict

- NO CYCLES EXECUTED in the baseline — the recording produced no qualified opportunities; no profitability statement can be made from it.

