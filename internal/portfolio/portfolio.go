// Package portfolio is the accounting lens over paper trading: realized
// P&L per starting asset, stranded-exposure positions with book-based
// mark-to-market, fee attribution per asset, equity/drawdown tracking,
// and snapshots (SKILL.md §23, §40–§41). Cash custody stays with the
// reservation manager; the portfolio never mutates balances directly.
package portfolio

import (
	"errors"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
)

// CashView is the reservation manager's read surface.
type CashView interface {
	Balance(asset exchange.Asset) (available, reserved decimal.Decimal)
}

// Marker values one asset amount in a target asset (simulation.Marker is
// satisfied by BookMarker below).
type Marker interface {
	Mark(asset exchange.Asset, amount decimal.Decimal, in exchange.Asset) (decimal.Decimal, bool)
}

// Portfolio aggregates settled cycle results.
type Portfolio struct {
	mu sync.Mutex

	cash    CashView
	initial map[exchange.Asset]decimal.Decimal // starting cash per start asset

	realized map[exchange.Asset]decimal.Decimal // per start asset
	exposure map[exchange.Asset]decimal.Decimal // stranded quantities per asset
	fees     map[exchange.Asset]decimal.Decimal // per fee asset, raw quantities

	cycles    int64
	completed int64
	failed    int64

	peak     map[exchange.Asset]decimal.Decimal // equity high-water mark per start asset
	drawdown map[exchange.Asset]decimal.Decimal // worst fraction from peak
}

// New creates a portfolio over the session's initial balances.
func New(cash CashView, initial map[exchange.Asset]decimal.Decimal) *Portfolio {
	p := &Portfolio{
		cash:     cash,
		initial:  make(map[exchange.Asset]decimal.Decimal, len(initial)),
		realized: make(map[exchange.Asset]decimal.Decimal),
		exposure: make(map[exchange.Asset]decimal.Decimal),
		fees:     make(map[exchange.Asset]decimal.Decimal),
		peak:     make(map[exchange.Asset]decimal.Decimal),
		drawdown: make(map[exchange.Asset]decimal.Decimal),
	}
	for a, v := range initial {
		p.initial[a] = v
		p.peak[a] = v
	}
	return p
}

var ErrShadowResult = errors.New("portfolio: shadow results are never applied")

// ApplyCycle records a settled cycle. Shadow results must not reach here
// (the caller routes them elsewhere); applying one is a programmer error.
func (p *Portfolio) ApplyCycle(res execution.CycleResult, shadow bool) error {
	if shadow {
		return ErrShadowResult
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cycles++
	if res.Outcome.Complete() {
		p.completed++
	} else if res.Outcome != execution.OutcomeRejected && res.Outcome != execution.OutcomeExpired {
		p.failed++
	}
	p.realized[res.StartAsset] = p.realized[res.StartAsset].Add(res.RealizedPnL)
	for asset, amt := range res.Exposure {
		p.exposure[asset] = p.exposure[asset].Add(amt)
	}
	for asset, amt := range res.Fees {
		p.fees[asset] = p.fees[asset].Add(amt)
	}
	return nil
}

// ReduceExposure records an unwind (exposure sold back; proceeds are
// credited to cash by the caller through the reservation manager).
func (p *Portfolio) ReduceExposure(asset exchange.Asset, amount decimal.Decimal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exposure[asset] = p.exposure[asset].Sub(amount)
	if p.exposure[asset].IsZero() {
		delete(p.exposure, asset)
	}
}

// Snapshot is the reporting view at one instant.
type Snapshot struct {
	At time.Time

	Cycles    int64
	Completed int64
	Failed    int64

	Realized   map[exchange.Asset]decimal.Decimal // per start asset
	Fees       map[exchange.Asset]decimal.Decimal
	Exposure   map[exchange.Asset]decimal.Decimal // raw quantities
	MarkValues map[exchange.Asset]decimal.Decimal // exposure valued per start asset
	Unmarked   []exchange.Asset                   // exposure with no mark available

	Equity   map[exchange.Asset]decimal.Decimal // cash + marked exposure, per start asset
	Drawdown map[exchange.Asset]decimal.Decimal // worst peak-to-trough fraction
}

// TakeSnapshot computes equity with current marks and advances the
// high-water mark / drawdown per starting asset. Unmarkable exposure is
// listed, never silently valued at zero without saying so.
func (p *Portfolio) TakeSnapshot(now time.Time, marker Marker) Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	snap := Snapshot{
		At: now, Cycles: p.cycles, Completed: p.completed, Failed: p.failed,
		Realized:   cloneMap(p.realized),
		Fees:       cloneMap(p.fees),
		Exposure:   cloneMap(p.exposure),
		MarkValues: make(map[exchange.Asset]decimal.Decimal),
		Equity:     make(map[exchange.Asset]decimal.Decimal),
		Drawdown:   make(map[exchange.Asset]decimal.Decimal),
	}

	for start := range p.initial {
		avail, reserved := p.cash.Balance(start)
		equity := avail.Add(reserved)
		for asset, amt := range p.exposure {
			if marker == nil {
				continue
			}
			if v, ok := marker.Mark(asset, amt, start); ok {
				equity = equity.Add(v)
				snap.MarkValues[start] = snap.MarkValues[start].Add(v)
			}
		}
		snap.Equity[start] = equity

		if equity.GreaterThan(p.peak[start]) {
			p.peak[start] = equity
		}
		if p.peak[start].IsPositive() {
			dd := p.peak[start].Sub(equity).Div(p.peak[start])
			if dd.GreaterThan(p.drawdown[start]) {
				p.drawdown[start] = dd
			}
		}
		snap.Drawdown[start] = p.drawdown[start]
	}
	// Unmarked exposure listing (any start asset's marker failure).
	for asset, amt := range p.exposure {
		if amt.IsZero() {
			continue
		}
		marked := false
		if marker != nil {
			for start := range p.initial {
				if _, ok := marker.Mark(asset, amt, start); ok {
					marked = true
					break
				}
			}
		}
		if !marked {
			snap.Unmarked = append(snap.Unmarked, asset)
		}
	}
	return snap
}

// Reset rebuilds the portfolio to a fresh session over new initial
// balances (BL-10, paper reset): realized P&L, exposure, fees, and cycle
// counters clear, and the high-water mark restarts at the new initial
// balances (not zero — a zeroed peak would report a fabricated drawdown
// on the very first snapshot after reset). It does not alter any of the
// accounting above: ApplyCycle/ReduceExposure/TakeSnapshot are untouched.
// The caller is responsible for ensuring no cycle is in flight when
// calling Reset (mirrors reservation.Manager.Reset).
func (p *Portfolio) Reset(initial map[exchange.Asset]decimal.Decimal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initial = make(map[exchange.Asset]decimal.Decimal, len(initial))
	p.realized = make(map[exchange.Asset]decimal.Decimal)
	p.exposure = make(map[exchange.Asset]decimal.Decimal)
	p.fees = make(map[exchange.Asset]decimal.Decimal)
	p.peak = make(map[exchange.Asset]decimal.Decimal, len(initial))
	p.drawdown = make(map[exchange.Asset]decimal.Decimal)
	p.cycles, p.completed, p.failed = 0, 0, 0
	for a, v := range initial {
		p.initial[a] = v
		p.peak[a] = v
	}
}

// State is the portfolio's complete accounting state, exported so a
// restart can continue the session instead of starting the ledger over
// (a restart must never reset a loss or a drawdown). Maps are copies.
type State struct {
	Realized map[exchange.Asset]decimal.Decimal // per start asset
	Peak     map[exchange.Asset]decimal.Decimal // equity high-water mark per start asset
	Drawdown map[exchange.Asset]decimal.Decimal // worst fraction per start asset
	Exposure map[exchange.Asset]decimal.Decimal // stranded quantities per asset
	Fees     map[exchange.Asset]decimal.Decimal // per fee asset

	Cycles, Completed, Failed int64
}

// State snapshots the accounting state.
func (p *Portfolio) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return State{
		Realized: cloneMap(p.realized), Peak: cloneMap(p.peak), Drawdown: cloneMap(p.drawdown),
		Exposure: cloneMap(p.exposure), Fees: cloneMap(p.fees),
		Cycles: p.cycles, Completed: p.completed, Failed: p.failed,
	}
}

// Restore replaces the accounting state with a persisted one (session
// resumption). The initial balances given to New stay as the key set;
// a start asset the state has no peak for keeps its initial balance as
// the high-water mark, exactly as a fresh portfolio would.
func (p *Portfolio) Restore(st State) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.realized = cloneMap(st.Realized)
	p.exposure = cloneMap(st.Exposure)
	p.fees = cloneMap(st.Fees)
	p.drawdown = cloneMap(st.Drawdown)
	p.peak = make(map[exchange.Asset]decimal.Decimal, len(p.initial))
	for a, v := range p.initial {
		p.peak[a] = v
	}
	for a, v := range st.Peak {
		p.peak[a] = v
	}
	p.cycles, p.completed, p.failed = st.Cycles, st.Completed, st.Failed
}

// Realized returns the realized session PnL for one start asset.
func (p *Portfolio) Realized(start exchange.Asset) decimal.Decimal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.realized[start]
}

// FeesPaid returns the cumulative simulated fees charged in one asset.
func (p *Portfolio) FeesPaid(asset exchange.Asset) decimal.Decimal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fees[asset]
}

// FeesMark values every fee asset's cumulative fees in one start asset
// through the marker (identity for the start asset itself). Legs 1 and
// 2 of a cycle charge their fee in the intermediate assets under a
// fee-in-received convention, so the start asset's own slice understates
// the cost of trading by most of it (audit F13). Unmarkable fee assets
// are listed and contribute nothing; the raw per-asset map is returned
// beside the total so nothing is hidden behind the valuation.
func (p *Portfolio) FeesMark(start exchange.Asset, marker Marker) (total decimal.Decimal, byAsset map[exchange.Asset]decimal.Decimal, unmarked []exchange.Asset) {
	p.mu.Lock()
	byAsset = cloneMap(p.fees)
	p.mu.Unlock()
	for asset, amt := range byAsset {
		if amt.IsZero() {
			continue
		}
		if asset == start {
			total = total.Add(amt)
			continue
		}
		if marker != nil {
			if v, ok := marker.Mark(asset, amt, start); ok {
				total = total.Add(v)
				continue
			}
		}
		unmarked = append(unmarked, asset)
	}
	return total, byAsset, unmarked
}

// ExposureMark values the stranded exposure in one start asset with the
// given marker. Unmarkable assets contribute nothing and are returned so
// the caller can say so; a nil marker marks nothing.
func (p *Portfolio) ExposureMark(start exchange.Asset, marker Marker) (decimal.Decimal, []exchange.Asset) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exposureMarkLocked(start, marker)
}

func (p *Portfolio) exposureMarkLocked(start exchange.Asset, marker Marker) (decimal.Decimal, []exchange.Asset) {
	var mark decimal.Decimal
	var unmarked []exchange.Asset
	for asset, amt := range p.exposure {
		if amt.IsZero() {
			continue
		}
		if marker != nil {
			if v, ok := marker.Mark(asset, amt, start); ok {
				mark = mark.Add(v)
				continue
			}
		}
		unmarked = append(unmarked, asset)
	}
	return mark, unmarked
}

// NetPnL is the session's realized (cash-basis) PnL plus the current mark
// of stranded exposure, per start asset. Realized alone treats a
// mid-cycle failure as a total loss of the deployed input even though the
// intermediate asset is still held; the marked figure is the economic
// position. Unmarkable exposure is valued at zero (the conservative
// side) and listed.
func (p *Portfolio) NetPnL(start exchange.Asset, marker Marker) (net decimal.Decimal, unmarked []exchange.Asset) {
	p.mu.Lock()
	defer p.mu.Unlock()
	mark, unmarked := p.exposureMarkLocked(start, marker)
	return p.realized[start].Add(mark), unmarked
}

// DailyLoss reports the current loss magnitude for the risk engine and
// the console (0 when flat or profitable): the negative part of NetPnL,
// so stranded exposure counts at its mark rather than as a total loss.
// A nil marker degrades to the cash basis, which over-states the loss —
// the safe direction for a limit.
func (p *Portfolio) DailyLoss(start exchange.Asset, marker Marker) decimal.Decimal {
	net, _ := p.NetPnL(start, marker)
	if net.IsNegative() {
		return net.Neg()
	}
	return decimal.Zero
}

// CurrentDrawdown for the risk context.
func (p *Portfolio) CurrentDrawdown(start exchange.Asset) decimal.Decimal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.drawdown[start]
}

func cloneMap(m map[exchange.Asset]decimal.Decimal) map[exchange.Asset]decimal.Decimal {
	out := make(map[exchange.Asset]decimal.Decimal, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// BookMarker marks assets through direct markets against the target
// asset: base=asset/quote=target sells into the bids; base=target/
// quote=asset buys from the asks (inverse). No multi-hop paths — an
// unmarkable asset is reported, not guessed
// (resources/execution-simulation.md).
//
// With Fees set the mark is a liquidation value: the position is walked
// through the book's depth as a taker order with the venue's fee, so a
// stranded position larger than the top level is valued at what
// unwinding it would actually return, and depth the book does not show
// counts for nothing. Without Fees the mark is the top-of-book price,
// which is optimistic for anything but dust (audit F16).
type BookMarker struct {
	Books   *orderbook.Set
	Markets []exchange.Market // instrument metadata for pair lookup
	Fees    *fees.Schedule    // optional: depth- and fee-aware liquidation marks
	Depth   int               // book depth for the liquidation walk (0 = full)
}

func (m BookMarker) Mark(asset exchange.Asset, amount decimal.Decimal, in exchange.Asset) (decimal.Decimal, bool) {
	if asset == in {
		return amount, true
	}
	for _, mk := range m.Markets {
		var leg graph.Leg
		switch {
		case mk.Base == asset && mk.Quote == in:
			leg = graph.Leg{Market: mk.ID, Base: mk.Base, Quote: mk.Quote, From: asset, To: in, Side: exchange.SideSell}
		case mk.Base == in && mk.Quote == asset:
			leg = graph.Leg{Market: mk.ID, Base: mk.Base, Quote: mk.Quote, From: asset, To: in, Side: exchange.SideBuy}
		default:
			continue
		}
		view, ok := m.Books.View(mk.ID, m.Depth)
		if !ok {
			continue
		}
		if m.Fees != nil && mk.Rules.Usable() {
			if v, ok := liquidationMark(leg, view, mk.Rules, m.Fees, amount); ok {
				return v, true
			}
		}
		if v, ok := topOfBookMark(leg.Side, view, amount); ok {
			return v, true
		}
	}
	return decimal.Zero, false
}

// liquidationMark walks the position through the book as a taker order.
// A position the visible depth cannot absorb is valued at the fillable
// part only — the conservative side. Amounts the venue's quantity step
// cannot express (dust) are left to the top-of-book fallback.
func liquidationMark(leg graph.Leg, view orderbook.View, rules exchange.InstrumentRules, sched *fees.Schedule, amount decimal.Decimal) (decimal.Decimal, bool) {
	lq, err := pricing.QuoteLeg(leg, pricing.MarketData{View: view, Rules: rules}, sched, amount)
	if err != nil {
		return decimal.Zero, false
	}
	return lq.NetOut, true
}

func topOfBookMark(side exchange.Side, view orderbook.View, amount decimal.Decimal) (decimal.Decimal, bool) {
	if side == exchange.SideSell {
		if bid, has := view.BestBid(); has {
			return amount.Mul(bid.Price), true
		}
		return decimal.Zero, false
	}
	if ask, has := view.BestAsk(); has && ask.Price.IsPositive() {
		return amount.Div(ask.Price), true
	}
	return decimal.Zero, false
}
