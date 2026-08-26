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
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
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

// DailyLoss reports the current loss magnitude vs initial for the risk
// engine (0 when flat/profitable). Computed on realized only — marked
// exposure swings are drawdown's business.
func (p *Portfolio) DailyLoss(start exchange.Asset) decimal.Decimal {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.realized[start]
	if r.IsNegative() {
		return r.Neg()
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
// asset: base=asset/quote=target uses the best bid; base=target/
// quote=asset uses the best ask (inverse). No multi-hop paths — an
// unmarkable asset is reported, not guessed
// (resources/execution-simulation.md).
type BookMarker struct {
	Books   *orderbook.Set
	Markets []exchange.Market // instrument metadata for pair lookup
}

func (m BookMarker) Mark(asset exchange.Asset, amount decimal.Decimal, in exchange.Asset) (decimal.Decimal, bool) {
	if asset == in {
		return amount, true
	}
	for _, mk := range m.Markets {
		switch {
		case mk.Base == asset && mk.Quote == in:
			if v, ok := m.Books.View(mk.ID, 1); ok {
				if bid, has := v.BestBid(); has {
					return amount.Mul(bid.Price), true
				}
			}
		case mk.Base == in && mk.Quote == asset:
			if v, ok := m.Books.View(mk.ID, 1); ok {
				if ask, has := v.BestAsk(); has && ask.Price.IsPositive() {
					return amount.Div(ask.Price), true
				}
			}
		}
	}
	return decimal.Zero, false
}
