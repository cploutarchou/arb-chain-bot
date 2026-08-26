// Package orderbook maintains local L2 books: decimal ladders with
// absolute-quantity semantics, venue-pluggable sequence validation, a
// health state machine, and copy-out views for the evaluator hot path
// (docs/research/market-data.md, docs/architecture.md §5–§6).
package orderbook

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// Level is one price level. Quantities are absolute (replacement) values;
// a zero quantity deletes the level.
type Level struct {
	Price decimal.Decimal
	Qty   decimal.Decimal
}

// DepthEvent is a normalized book message from any venue decoder: either a
// snapshot (replace the book) or a delta (merge levels). Venue sequence
// fields are carried verbatim for the venue's SequenceValidator; unused
// fields stay zero.
type DepthEvent struct {
	Market     exchange.MarketID
	IsSnapshot bool

	FirstUpdateID int64 // e.g. Binance U, Gate U
	FinalUpdateID int64 // e.g. Binance u, OKX seqId, snapshot lastUpdateId
	PrevFinalID   int64 // e.g. OKX prevSeqId (0 when venue has none)

	Bids []Level
	Asks []Level

	EventTime   time.Time // exchange event time; zero when venue omits it
	ReceiveTime time.Time // local receive timestamp (transport stamps it)
}

// Action is a SequenceValidator verdict for one delta event.
type Action uint8

const (
	ActionApply Action = iota + 1 // event extends the book: merge it
	ActionDrop                    // stale duplicate: ignore silently
	ActionGap                     // continuity broken: book is CORRUPTED, resync
	ActionReset                   // event replaces the book (in-band snapshot/reset)
)

func (a Action) String() string {
	switch a {
	case ActionApply:
		return "APPLY"
	case ActionDrop:
		return "DROP"
	case ActionGap:
		return "GAP"
	case ActionReset:
		return "RESET"
	default:
		return "UNKNOWN"
	}
}

// Meta is the validator-visible book state.
type Meta struct {
	State         State
	LastUpdateID  int64
	Initialized   bool // a snapshot has been applied since the last reset
}

// SequenceValidator encodes one venue's continuity rule
// (docs/research/market-data.md §2). Implementations must be pure:
// same (meta, event) always yields the same Action.
type SequenceValidator interface {
	Validate(meta Meta, ev DepthEvent) Action
}
