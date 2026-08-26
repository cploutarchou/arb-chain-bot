// Package replay runs one recorded market-data session through the
// REAL scanner (console BL-17: "console-driven replay runs"). It is
// deliberately thin: internal/backtest already drives a recording
// through marketdata.Replayer → the scanner → risk → the replay
// executor deterministically (T-046), and internal/campaign already
// shows the background-job shape this needs (queue one, run it,
// publish progress, persist the row so a page reload survives it).
// A replay is exactly that machinery with a single baseline scenario
// and, optionally, a specific persisted strategy version — the CLI
// (`make campaign`) and the console share internal/campaign the same
// way; this package is that same sharing for a single ad-hoc replay.
package replay

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/backtest"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange/binance"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

var recordingIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// replaySeed roots the deterministic latency draw. A replay run always
// asks the same question ("what does the CURRENT strategy do against
// this recording") so, unlike a campaign's multi-seed grid, one fixed
// seed is the honest choice: varying it would just add noise to a
// single-run answer nobody asked for.
const replaySeed int64 = 1

// startingAsset is the only starting asset a console-driven replay
// evaluates. The task's request shape is deliberately narrow
// (recording, config_version, speed) — campaigns already own the
// full assets/balances/fee-bps grid surface; a replay is meant to be a
// one-click "run the CURRENT strategy against a recording" check, not
// a second campaign configuration surface.
var startingAsset = exchange.Asset("USDT")

var startingBalance = decimal.NewFromInt(10000)

// Request is one replay's parameters.
type Request struct {
	Recording string `json:"recording"`
	// ConfigVersion, when non-zero, pins the run to a persisted strategy
	// version instead of whatever is live when the run starts. A version
	// that fails to resolve fails the RUN with that reason — it is never
	// silently substituted with defaults (unlike backtest.Run's own
	// zero-Params fallback, which this package deliberately does not
	// inherit for an explicitly requested version).
	ConfigVersion int64 `json:"config_version,omitempty"`
	// Speed is accepted and persisted for the console's audit trail and
	// forward compatibility, but has NO effect on execution today:
	// backtest.Run (what this package calls) is a deterministic,
	// single-threaded discrete-event replay — it steps to the next
	// recorded frame or latency deadline, not to wall-clock time, so
	// there is no "pace" to scale. A real-time-paced replay would need
	// to drive marketdata.Replayer directly against a live scanner
	// instance, which is a materially different (and materially
	// heavier) feature than this endpoint promises; documented here and
	// in docs/MASTER_PLAN.md T-058 rather than silently ignored.
	Speed float64 `json:"speed,omitempty"`
}

// Normalize fills defaults and validates.
func (r Request) Normalize() (Request, error) {
	r.Recording = strings.TrimSpace(r.Recording)
	if r.Recording == "" {
		return r, fmt.Errorf("replay: recording is required")
	}
	// The id names a directory under the recordings root: keep it to the
	// same alphabet campaign.Request uses so a request can never escape
	// the root.
	if !recordingIDRe.MatchString(r.Recording) {
		return r, fmt.Errorf("replay: recording id %q is not a valid session id", r.Recording)
	}
	if r.ConfigVersion < 0 {
		return r, fmt.Errorf("replay: config_version must be positive")
	}
	if r.Speed < 0 {
		return r, fmt.Errorf("replay: speed must be positive")
	}
	if r.Speed > 1000 {
		return r, fmt.Errorf("replay: speed out of range")
	}
	return r, nil
}

// Sources is the metadata a replay needs from persistence — the same
// shape internal/campaign.Sources needs, kept as its own declaration so
// this package does not import campaign for an interface (storage.Store
// already implements both independently).
type Sources interface {
	RecordingStreams(ctx context.Context, recordingID string) (map[uint16]exchange.Symbol, error)
	LoadMarkets(ctx context.Context, exchangeID exchange.ExchangeID) ([]exchange.Market, error)
}

// ParamsSource resolves a persisted strategy version — only the one
// method of *strategy.Service a replay needs, so a caller can wire a
// nil-checked fake in tests without pulling in the full service.
type ParamsSource interface {
	Get(ctx context.Context, version int64) (strategy.Snapshot, error)
}

// Progress is one status update; a replay is a single backtest.Run call
// so, unlike a campaign's per-(scenario,seed) steps, it only reports
// Done=0/Total=1 (queued) and Done=1/Total=1 (finished) — still enough
// for the console to show "running" vs "done" while it waits.
type Progress struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Step  string `json:"step,omitempty"`
}

// TopOpportunity is one executed cycle from the run, ranked by realized
// net bps. This is a PROXY for "top opportunities": backtest.Run does
// not expose a raw list of qualified-but-unexecuted opportunities, only
// the cycles it actually attempted to execute past risk and reservation
// — and a cycle can still fail after that point (reservation conflict,
// TTL expiry), so Result.Qualified and len(Cycles) legitimately
// disagree. Both numbers are reported; Top is documented as "executed
// cycles", not "qualified opportunities", in the API response.
type TopOpportunity struct {
	OpportunityID string          `json:"opportunity_id"`
	TriangleID    string          `json:"triangle_id"`
	Outcome       string          `json:"outcome"`
	NetBps        decimal.Decimal `json:"net_bps"`
	At            time.Time       `json:"at"`
}

// Result is one replay's outcome.
type Result struct {
	Evaluations int64            `json:"evaluations"`
	Qualified   int64            `json:"qualified"`
	Cycles      int64            `json:"cycles"`
	Top         []TopOpportunity `json:"top"`
}

const topN = 10

// Execute runs one replay against the recording's segments in dir.
// progress (optional) is called at start and finish; ctx cancellation
// is honored between the (single) backtest.Run call — there is no
// finer-grained cancellation point inside backtest.Run itself.
func Execute(ctx context.Context, src Sources, params ParamsSource, dir string, req Request, progress func(Progress)) (Result, error) {
	req, err := req.Normalize()
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	segments, err := filepath.Glob(filepath.Join(dir, "depth-*.seg.zst"))
	if err != nil || len(segments) == 0 {
		return Result{}, fmt.Errorf("no segments in %s (glob depth-*.seg.zst): %v", dir, err)
	}
	sort.Strings(segments)

	streams, err := src.RecordingStreams(ctx, req.Recording)
	if err != nil {
		return Result{}, err
	}
	allMarkets, err := src.LoadMarkets(ctx, binance.ID)
	if err != nil {
		return Result{}, err
	}
	recorded := map[exchange.Symbol]bool{}
	for _, sym := range streams {
		recorded[sym] = true
	}
	var markets []exchange.Market
	for _, m := range allMarkets {
		if recorded[m.ID.Symbol] {
			markets = append(markets, m)
		}
	}
	if len(markets) < 3 {
		return Result{}, fmt.Errorf("only %d of the recorded symbols have market metadata; run the recorder with persistence so markets sync", len(markets))
	}

	stratParams := strategy.DefaultParams()
	if req.ConfigVersion != 0 {
		if params == nil {
			return Result{}, fmt.Errorf("replay: config_version %d requested but no strategy service is configured in this profile", req.ConfigVersion)
		}
		snap, err := params.Get(ctx, req.ConfigVersion)
		if err != nil {
			return Result{}, fmt.Errorf("replay: config_version %d: %w", req.ConfigVersion, err)
		}
		stratParams = snap.Params
	}

	if progress != nil {
		progress(Progress{Done: 0, Total: 1, Step: "running"})
	}
	res, err := backtest.Run(backtest.Options{
		Segments:        segments,
		Streams:         streams,
		Markets:         markets,
		StartingAssets:  []exchange.Asset{startingAsset},
		InitialBalances: map[exchange.Asset]decimal.Decimal{startingAsset: startingBalance},
		Params:          stratParams,
		Seed:            replaySeed,
		Scenario:        backtest.Scenario{}, // zero value normalizes to the unstressed baseline
	})
	if err != nil {
		return Result{}, err
	}
	if progress != nil {
		progress(Progress{Done: 1, Total: 1})
	}

	cycles := make([]backtest.CycleRecord, len(res.Cycles))
	copy(cycles, res.Cycles)
	sort.SliceStable(cycles, func(i, j int) bool { return cycles[i].NetBps.GreaterThan(cycles[j].NetBps) })
	if len(cycles) > topN {
		cycles = cycles[:topN]
	}
	top := make([]TopOpportunity, 0, len(cycles))
	for _, c := range cycles {
		top = append(top, TopOpportunity{
			OpportunityID: c.OpportunityID, TriangleID: c.TriangleID,
			Outcome: c.Outcome, NetBps: c.NetBps, At: c.At,
		})
	}
	return Result{Evaluations: res.Evaluations, Qualified: res.Qualified, Cycles: int64(len(res.Cycles)), Top: top}, nil
}
