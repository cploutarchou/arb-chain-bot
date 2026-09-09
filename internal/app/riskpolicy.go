package app

import (
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/portfolio"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

// Breaker names the engine's policies manage (docs/risk.md §3, §5). The
// registry itself is policy-free: it records states and transitions;
// what opens and closes each breaker lives here.
const (
	breakerPersistence   = "persistence"              // outbox write failures (engine.go)
	breakerInconsistency = "simulation_inconsistency" // ledger invariant violation; operator-closed
	breakerDailyLoss     = "daily_loss"               // session loss at the limit; operator-closed
	breakerDrawdown      = "drawdown"                 // peak-to-trough at the limit; operator-closed
	breakerFeed          = "feed_instability"         // repeated book faults; probes and closes itself
	breakerSlippage      = "slippage"                 // realized slippage past the limit; operator-closed
)

// slippageBreaches is how many consecutive completed cycles must realize
// more slippage than max_slippage_bps before the slippage breaker opens:
// one outlier is a market event, three in a row say the execution model
// no longer describes the market (docs/risk.md §3, "large unexpected
// slippage, measured vs modelled").
const slippageBreaches = 3

// Feed-instability policy: a book fault is a transition to CORRUPTED
// (sequence gap, integrity loss) or DISCONNECTED (transport lost). One
// transport loss marks every book at once, so faults closer together
// than faultCoalesce count as one event. feedFaultThreshold events
// inside feedFaultWindow open the exchange-scoped breaker; after
// feedProbeAfter it moves to HALF_OPEN (qualification resumes), and a
// further feedProbeAfter without a fault closes it, while any fault
// while HALF_OPEN reopens it.
const (
	feedFaultThreshold = 5
	feedFaultWindow    = time.Minute
	feedProbeAfter     = 30 * time.Second
	faultCoalesce      = time.Second
)

// faultWindow is a coalescing sliding-window event counter.
type faultWindow struct {
	mu     sync.Mutex
	window time.Duration
	times  []time.Time
}

func newFaultWindow(window time.Duration) *faultWindow {
	return &faultWindow{window: window}
}

// add records a fault at now (coalesced with the previous one when closer
// than faultCoalesce) and returns the count inside the window.
func (w *faultWindow) add(now time.Time) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pruneLocked(now)
	if n := len(w.times); n == 0 || now.Sub(w.times[n-1]) >= faultCoalesce {
		w.times = append(w.times, now)
	}
	return len(w.times)
}

// count returns the faults inside the window ending at now.
func (w *faultWindow) count(now time.Time) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pruneLocked(now)
	return len(w.times)
}

// last returns the most recent fault time (zero when none in the window).
func (w *faultWindow) last(now time.Time) time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pruneLocked(now)
	if n := len(w.times); n > 0 {
		return w.times[n-1]
	}
	return time.Time{}
}

func (w *faultWindow) pruneLocked(now time.Time) {
	cut := now.Add(-w.window)
	i := 0
	for i < len(w.times) && !w.times[i].After(cut) {
		i++
	}
	if i > 0 {
		w.times = append(w.times[:0], w.times[i:]...)
	}
}

// feedFaultObserver is the book-transition hook: it feeds the fault
// window and opens the exchange breaker at the threshold.
func feedFaultObserver(reg *risk.Registry, faults *faultWindow, now func() time.Time) func(id exchange.MarketID, from, to orderbook.State, reason string) {
	return func(id exchange.MarketID, _, to orderbook.State, reason string) {
		if to != orderbook.StateCorrupted && to != orderbook.StateDisconnected {
			return
		}
		t := now()
		n := faults.add(t)
		if n >= feedFaultThreshold {
			reg.Trip(breakerFeed, "exchange:"+string(id.Exchange),
				fmt.Sprintf("%d book faults within %s (latest %s: %s %s)", n, feedFaultWindow, id.Symbol, to, reason), t)
		}
	}
}

// feedPolicy runs on the engine tick (single goroutine): it moves an
// eligible OPEN breaker to HALF_OPEN, closes a HALF_OPEN one that has
// stayed quiet for feedProbeAfter since the probe, and reopens one that
// faulted after the probe. Faults that happened while OPEN do not count
// against the probe — otherwise a breaker that faulted once while open
// could never recover.
type feedPolicy struct {
	reg      *risk.Registry
	faults   *faultWindow
	scope    string
	probedAt time.Time
}

func (p *feedPolicy) tick(now time.Time) {
	if p.reg.Probe(breakerFeed, p.scope, now) {
		p.probedAt = now
		return
	}
	state, _ := breakerState(p.reg, breakerFeed, p.scope)
	if state != risk.BreakerHalfOpen {
		return
	}
	last := p.faults.last(now)
	switch {
	case !last.IsZero() && last.After(p.probedAt):
		p.reg.Trip(breakerFeed, p.scope, fmt.Sprintf("book fault while probing (%s)", last.Format(time.RFC3339)), now)
	case now.Sub(p.probedAt) >= feedProbeAfter:
		p.reg.Close(breakerFeed, p.scope, now)
	}
}

// breakerState reads one breaker's state and the time it opened.
func breakerState(reg *risk.Registry, name, scope string) (risk.BreakerState, time.Time) {
	for _, tr := range reg.States() {
		if tr.Name == name && tr.Scope == scope {
			return tr.To, tr.At
		}
	}
	return risk.BreakerClosed, time.Time{}
}

// sessionLedger is the scanner's LedgerView over the paper portfolio:
// the session loss and drawdown per start asset, refreshed after every
// settlement and on the engine tick rather than computed per evaluation
// (marking stranded exposure walks the books, and the scanner evaluates
// thousands of triangles a second). refresh also advances the
// portfolio's high-water mark, which previously only moved when a
// console poll happened to take a snapshot.
type sessionLedger struct {
	mu   sync.RWMutex
	loss map[exchange.Asset]decimal.Decimal
	dd   map[exchange.Asset]decimal.Decimal
}

func newSessionLedger() *sessionLedger {
	return &sessionLedger{loss: map[exchange.Asset]decimal.Decimal{}, dd: map[exchange.Asset]decimal.Decimal{}}
}

func (l *sessionLedger) DailyLoss(start exchange.Asset) decimal.Decimal {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.loss[start]
}

func (l *sessionLedger) Drawdown(start exchange.Asset) decimal.Decimal {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.dd[start]
}

// refresh recomputes both figures and applies the loss policy: at or
// past a positive limit the corresponding global breaker opens, which
// pauses qualification until an operator closes it (docs/risk.md §5 —
// automatic resume is off).
func (l *sessionLedger) refresh(now time.Time, port *portfolio.Portfolio, marker portfolio.Marker,
	starts []exchange.Asset, lim risk.Limits, reg *risk.Registry) {
	if port == nil {
		return
	}
	snap := port.TakeSnapshot(now, marker)
	l.mu.Lock()
	for _, a := range starts {
		l.loss[a] = port.DailyLoss(a, marker)
		l.dd[a] = snap.Drawdown[a]
	}
	loss, dd := cloneAssets(l.loss), cloneAssets(l.dd)
	l.mu.Unlock()
	if reg == nil {
		return
	}
	for _, a := range starts {
		if lim.MaxDailyLoss.IsPositive() && loss[a].GreaterThanOrEqual(lim.MaxDailyLoss) {
			reg.Trip(breakerDailyLoss, "", fmt.Sprintf("%s session loss %s reached the limit %s",
				a, loss[a].String(), lim.MaxDailyLoss.String()), now)
		}
		if lim.MaxDrawdown.IsPositive() && dd[a].GreaterThanOrEqual(lim.MaxDrawdown) {
			reg.Trip(breakerDrawdown, "", fmt.Sprintf("%s drawdown %s reached the limit %s",
				a, dd[a].StringFixed(4), lim.MaxDrawdown.String()), now)
		}
	}
}

func cloneAssets(m map[exchange.Asset]decimal.Decimal) map[exchange.Asset]decimal.Decimal {
	out := make(map[exchange.Asset]decimal.Decimal, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// slippagePolicy compares realized cycle slippage with the strategy's
// max_slippage_bps at every settlement. Only cycles that returned to the
// start asset carry a measurable slippage; the others neither count nor
// reset the streak.
type slippagePolicy struct {
	mu     sync.Mutex
	streak int
}

func (p *slippagePolicy) observe(res execution.CycleResult, lim risk.Limits, reg *risk.Registry, now time.Time) {
	if !res.Outcome.Complete() || !lim.MaxSlippageBps.IsPositive() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if res.SlippageBps.LessThanOrEqual(lim.MaxSlippageBps) {
		p.streak = 0
		return
	}
	p.streak++
	if p.streak >= slippageBreaches && reg != nil {
		reg.Trip(breakerSlippage, "", fmt.Sprintf("%d consecutive cycles realized more than %s bps of slippage (latest %s bps, cycle %s)",
			p.streak, lim.MaxSlippageBps.String(), res.SlippageBps.StringFixed(2), res.CycleID), now)
	}
}
