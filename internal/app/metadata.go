package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

// Metadata monitor (audit trading-logic T8): the topology and the
// instrument rules the engine prices against are built once per run
// from the venue's exchangeInfo. A symbol moving to BREAK/HALT, a
// LOT_SIZE or NOTIONAL change, or a delisting is invisible until a
// restart — the staleness gate catches a halted stream, never a filter
// change. Rather than hot-swapping the topology mid-run (the books, the
// triangle graph, the depth search and every in-flight reservation all
// key off it), the monitor re-fetches the venue's metadata on a timer,
// diffs it against the run's own baseline and opens the operator-closed
// metadata_changed breaker on any material change — qualification stops
// executing against rules the venue no longer enforces until an
// operator restarts (or, knowing the diff, closes the breaker again).
const breakerMetadata = "metadata_changed"

// metadataDiff is the material difference between the run's baseline
// metadata and a fresh venue snapshot, scoped to the symbols this run
// actually trades (a new listing the operator has not configured is not
// material to this run; a delisted/halted/re-filtered configured one
// is).
type metadataDiff struct {
	Gone     []string // baseline symbols missing from the fresh snapshot (delisted/unlisted)
	Status   []string // trading status changed (e.g. TRADING → BREAK/HALT)
	Filter   []string // instrument rules changed (tick, step, min/max, notional)
	Examples []string // up to 3 human-readable examples for the reason string
}

func (d metadataDiff) empty() bool {
	return len(d.Gone) == 0 && len(d.Status) == 0 && len(d.Filter) == 0
}

func (d metadataDiff) summary() string {
	var parts []string
	if n := len(d.Gone); n > 0 {
		parts = append(parts, fmt.Sprintf("%d no longer listed (%s)", n, joinCapped(d.Gone, 3)))
	}
	if n := len(d.Status); n > 0 {
		parts = append(parts, fmt.Sprintf("%d changed trading status (%s)", n, joinCapped(d.Status, 3)))
	}
	if n := len(d.Filter); n > 0 {
		parts = append(parts, fmt.Sprintf("%d changed instrument filters (%s)", n, joinCapped(d.Filter, 3)))
	}
	return strings.Join(parts, "; ")
}

func joinCapped(items []string, cap int) string {
	if len(items) <= cap {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:cap], ", ") + "…"
}

// rulesEqual compares two instrument rules exactly. decimal.Equals, not
// SamePointing: "0.001" and "1e-3" are the same step and must not read
// as a venue change.
func rulesEqual(a, b exchange.InstrumentRules) bool {
	if a.QtyMode != b.QtyMode || a.PriceMode != b.PriceMode ||
		a.QtyDecimals != b.QtyDecimals || a.PriceDecimals != b.PriceDecimals {
		return false
	}
	for _, p := range [][2]decimal.Decimal{
		{a.QtyStep, b.QtyStep}, {a.PriceTick, b.PriceTick},
		{a.MinQty, b.MinQty}, {a.MaxQty, b.MaxQty},
		{a.MinNotional, b.MinNotional}, {a.MaxNotional, b.MaxNotional},
		{a.MarketQtyStep, b.MarketQtyStep}, {a.MarketMinQty, b.MarketMinQty}, {a.MarketMaxQty, b.MarketMaxQty},
	} {
		if !p[0].Equal(p[1]) {
			return false
		}
	}
	return true
}

// diffMetadata compares the baseline (the scoped markets this run
// trades) with a fresh full snapshot, keyed by symbol.
func diffMetadata(baseline map[string]exchange.Market, fresh map[string]exchange.Market) metadataDiff {
	var out metadataDiff
	syms := make([]string, 0, len(baseline))
	for s := range baseline {
		syms = append(syms, s)
	}
	sort.Strings(syms)
	for _, s := range syms {
		base := baseline[s]
		cur, ok := fresh[s]
		if !ok {
			out.Gone = append(out.Gone, s)
			out.Examples = append(out.Examples, fmt.Sprintf("%s no longer listed", s))
			continue
		}
		if base.Status != cur.Status {
			out.Status = append(out.Status, fmt.Sprintf("%s %s→%s", s, base.Status, cur.Status))
			out.Examples = append(out.Examples, fmt.Sprintf("%s status %s→%s", s, base.Status, cur.Status))
			continue
		}
		if !rulesEqual(base.Rules, cur.Rules) {
			out.Filter = append(out.Filter, s)
			out.Examples = append(out.Examples, fmt.Sprintf("%s instrument filters changed", s))
		}
	}
	if len(out.Examples) > 3 {
		out.Examples = out.Examples[:3]
	}
	return out
}

// watchMetadata is the T8 monitor loop: one bounded goroutine per run,
// cancelled with the run. A failed fetch is logged and retried on the
// next tick — a venue/API hiccup must never look like a metadata change
// — and an unchanged snapshot is silent. On a material diff the
// exchange-scoped breaker opens (the scanner gate honours the scope),
// with a reason that names the change; the operator restarts to rebuild
// or closes the breaker from the Risk Center knowing the diff (it
// re-opens on the next tick while the diff persists, like the loss
// breakers re-arm at their limits).
func (e *Engine) watchMetadata(ctx context.Context, rest metadataSource, reg *risk.Registry, scope string,
	baseline map[string]exchange.Market, interval time.Duration, now func() time.Time) {
	if interval <= 0 || reg == nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			markets, err := rest.ExchangeInfo(fetchCtx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					e.log.Warn("metadata re-check failed; retrying next interval", "error", err)
				}
				continue
			}
			fresh := make(map[string]exchange.Market, len(markets))
			for _, m := range markets {
				fresh[string(m.ID.Symbol)] = m
			}
			diff := diffMetadata(baseline, fresh)
			if diff.empty() {
				continue
			}
			reg.Trip(breakerMetadata, scope,
				fmt.Sprintf("venue metadata changed since this run started: %s — topology and instrument rules are stale; restart to rebuild (the breaker re-opens while the change persists)", diff.summary()),
				now())
		}
	}
}

// metadataSource is the venue-metadata half of the Binance REST client
// the monitor needs (tests fake it).
type metadataSource interface {
	ExchangeInfo(ctx context.Context) ([]exchange.Market, error)
}
