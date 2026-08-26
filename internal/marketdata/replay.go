package marketdata

import (
	"fmt"
	"strings"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange/binance"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

// Replayer drives recorded frames through the SAME decoder → syncer →
// book path as the live feed (SKILL.md §64): same segments + same code ⇒
// identical book evolution. REST frames carry the symbol in StreamID via
// the recorder's stream table.
type Replayer struct {
	Books   *orderbook.Set
	Streams map[uint16]exchange.Symbol // recorder's stream table
	// OnApply, when set, observes every applied change (decision logging,
	// scanner driving in BACKTEST wiring).
	OnApply func(id exchange.MarketID, action orderbook.Action)

	syncers map[exchange.MarketID]*binance.Syncer
}

// NewReplayer prepares books for the recorded symbols.
func NewReplayer(streams map[uint16]exchange.Symbol) *Replayer {
	r := &Replayer{
		Books:   orderbook.NewSet(),
		Streams: streams,
		syncers: make(map[exchange.MarketID]*binance.Syncer, len(streams)),
	}
	for _, sym := range streams {
		id := exchange.MarketID{Exchange: binance.ID, Symbol: sym}
		book := orderbook.New(id, 0)
		r.Books.Add(book)
		r.syncers[id] = binance.NewSyncer(book, 0)
	}
	return r
}

// Apply consumes one recorded frame.
func (r *Replayer) Apply(fr Frame) error {
	switch fr.Dir {
	case DirWS:
		ev, err := binance.DecodeWSFrame(fr.Payload, fr.Recv)
		if err != nil {
			if strings.Contains(err.Error(), "not a depth event") {
				return nil // other stream types recorded alongside depth
			}
			return fmt.Errorf("replay: ws frame: %w", err)
		}
		syncer, ok := r.syncers[ev.Market]
		if !ok {
			return nil // unsubscribed market in a shared recording
		}
		action, err := syncer.OnDelta(ev)
		if err != nil {
			return fmt.Errorf("replay: %s: %w", ev.Market, err)
		}
		if r.OnApply != nil {
			r.OnApply(ev.Market, action)
		}
		if action == orderbook.ActionApply || action == orderbook.ActionReset {
			r.Books.MarkDirty(ev.Market)
		}
	case DirREST:
		sym, ok := r.Streams[fr.StreamID]
		if !ok {
			return fmt.Errorf("replay: unknown REST stream id %d", fr.StreamID)
		}
		ev, err := binance.DecodeRESTSnapshot(sym, fr.Payload, fr.Recv)
		if err != nil {
			return fmt.Errorf("replay: snapshot: %w", err)
		}
		id := exchange.MarketID{Exchange: binance.ID, Symbol: sym}
		syncer, ok := r.syncers[id]
		if !ok {
			return nil
		}
		if err := syncer.OnSnapshot(ev); err != nil {
			// A behind-buffer snapshot in a recording reflects what live saw;
			// the recording also carries the later snapshot that healed it.
			return nil //nolint:nilerr // faithful to live behavior
		}
		if r.OnApply != nil {
			r.OnApply(id, orderbook.ActionReset)
		}
		r.Books.MarkDirty(id)
	default:
		return fmt.Errorf("replay: unknown direction %d", fr.Dir)
	}
	return nil
}

// ReplaySegments runs whole files in order.
func (r *Replayer) ReplaySegments(paths []string) error {
	for _, p := range paths {
		if err := ReadSegment(p, r.Apply); err != nil {
			return fmt.Errorf("replay %s: %w", p, err)
		}
	}
	return nil
}

// Fingerprint summarizes the deterministic end-state of all books —
// replays of the same recording must produce identical fingerprints.
func (r *Replayer) Fingerprint() string {
	var b strings.Builder
	ids := r.Books.All()
	// Sorted for stability.
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j].String() < ids[i].String() {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	for _, id := range ids {
		v, _ := r.Books.View(id, 0)
		fmt.Fprintf(&b, "%s|state=%s|ver=%d|", id, v.State, v.Version)
		for _, l := range v.Bids {
			fmt.Fprintf(&b, "b%s@%s,", l.Qty, l.Price)
		}
		for _, l := range v.Asks {
			fmt.Fprintf(&b, "a%s@%s,", l.Qty, l.Price)
		}
		b.WriteString(";")
	}
	return b.String()
}
