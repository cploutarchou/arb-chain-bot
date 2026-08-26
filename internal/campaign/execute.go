// Package campaign runs the §80 profitability-validation campaign
// (docs/deployment.md §3) over one recording: every (scenario × seed) of
// the stress grid through internal/backtest, then the honest report.
// The CLI (cmd/campaign) and the console API share this one
// implementation so a run means the same thing wherever it is started.
package campaign

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/backtest"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange/binance"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
)

var recordingIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Request is one campaign's parameters. Zero values take the runbook
// defaults (USDT, USDT=10000, seeds 1,2,3, 10/10 bps, full grid).
type Request struct {
	Recording string            `json:"recording"`
	Assets    []string          `json:"assets,omitempty"`
	Balances  map[string]string `json:"balances,omitempty"`
	Seeds     []int64           `json:"seeds,omitempty"`
	MakerBps  int64             `json:"fee_maker_bps,omitempty"`
	TakerBps  int64             `json:"fee_taker_bps,omitempty"`
	Grid      string            `json:"grid,omitempty"` // full | baseline
}

// Normalize fills defaults and validates. It returns the request the
// run will actually use, so the persisted record is self-describing.
func (r Request) Normalize() (Request, error) {
	r.Recording = strings.TrimSpace(r.Recording)
	if r.Recording == "" {
		return r, fmt.Errorf("campaign: recording is required")
	}
	// The id names a directory under the recordings root: keep it to the
	// ULID alphabet so a request can never escape the root.
	if !recordingIDRe.MatchString(r.Recording) {
		return r, fmt.Errorf("campaign: recording id %q is not a valid session id", r.Recording)
	}
	if len(r.Assets) == 0 {
		r.Assets = []string{"USDT"}
	}
	assets := make([]string, 0, len(r.Assets))
	for _, a := range r.Assets {
		if a = strings.ToUpper(strings.TrimSpace(a)); a != "" {
			assets = append(assets, a)
		}
	}
	if len(assets) == 0 {
		return r, fmt.Errorf("campaign: at least one starting asset is required")
	}
	r.Assets = assets
	if len(r.Balances) == 0 {
		r.Balances = map[string]string{assets[0]: "10000"}
	}
	bal := make(map[string]string, len(r.Balances))
	for k, v := range r.Balances {
		amt, err := decimal.NewFromString(strings.TrimSpace(v))
		if err != nil || !amt.IsPositive() {
			return r, fmt.Errorf("campaign: bad balance %s=%q", k, v)
		}
		bal[strings.ToUpper(strings.TrimSpace(k))] = amt.String()
	}
	r.Balances = bal
	if len(r.Seeds) == 0 {
		r.Seeds = []int64{1, 2, 3}
	}
	if len(r.Seeds) > 16 {
		return r, fmt.Errorf("campaign: at most 16 seeds")
	}
	if r.MakerBps == 0 {
		r.MakerBps = 10
	}
	if r.TakerBps == 0 {
		r.TakerBps = 10
	}
	if r.MakerBps < 0 || r.TakerBps < 0 || r.MakerBps > 1000 || r.TakerBps > 1000 {
		return r, fmt.Errorf("campaign: fee bps out of range")
	}
	switch r.Grid {
	case "":
		r.Grid = "full"
	case "full", "baseline":
	default:
		return r, fmt.Errorf("campaign: grid must be full or baseline")
	}
	return r, nil
}

// Total is the number of backtest runs the request implies.
func (r Request) Total() int {
	grid := len(backtest.DefaultGrid())
	if r.Grid == "baseline" {
		grid = 1
	}
	return grid * len(r.Seeds)
}

// Sources is the metadata the campaign needs from persistence.
type Sources interface {
	RecordingStreams(ctx context.Context, recordingID string) (map[uint16]exchange.Symbol, error)
	LoadMarkets(ctx context.Context, exchangeID exchange.ExchangeID) ([]exchange.Market, error)
}

// Progress is one completed (scenario, seed) step.
type Progress struct {
	Done     int    `json:"done"`
	Total    int    `json:"total"`
	Scenario string `json:"scenario"`
	Seed     int64  `json:"seed"`
}

// Execute runs the grid over the recording's segments in dir. progress
// (optional) is called after every step; ctx cancellation aborts between
// steps. The returned Campaign is complete only when err is nil.
func Execute(ctx context.Context, src Sources, dir string, req Request, progress func(Progress)) (*backtest.Campaign, error) {
	req, err := req.Normalize()
	if err != nil {
		return nil, err
	}
	segments, err := filepath.Glob(filepath.Join(dir, "depth-*.seg.zst"))
	if err != nil || len(segments) == 0 {
		return nil, fmt.Errorf("no segments in %s (glob depth-*.seg.zst): %v", dir, err)
	}
	sort.Strings(segments)

	streams, err := src.RecordingStreams(ctx, req.Recording)
	if err != nil {
		return nil, err
	}
	allMarkets, err := src.LoadMarkets(ctx, binance.ID)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("only %d of the recorded symbols have market metadata; run the recorder with persistence so markets sync", len(markets))
	}

	startAssets := make([]exchange.Asset, 0, len(req.Assets))
	for _, a := range req.Assets {
		startAssets = append(startAssets, exchange.Asset(a))
	}
	initial := make(map[exchange.Asset]decimal.Decimal, len(req.Balances))
	for k, v := range req.Balances {
		initial[exchange.Asset(k)] = decimal.RequireFromString(v)
	}
	grid := backtest.DefaultGrid()
	if req.Grid == "baseline" {
		grid = grid[:1]
	}
	baseFees := fees.Rate{
		Maker: decimal.NewFromInt(req.MakerBps).Div(decimal.NewFromInt(10_000)),
		Taker: decimal.NewFromInt(req.TakerBps).Div(decimal.NewFromInt(10_000)),
	}

	campaign := &backtest.Campaign{Recording: req.Recording, GeneratedAt: time.Now().UTC()}
	total := len(grid) * len(req.Seeds)
	n := 0
	for _, sc := range grid {
		for _, seed := range req.Seeds {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			res, err := backtest.Run(backtest.Options{
				Segments:        segments,
				Streams:         streams,
				Markets:         markets,
				StartingAssets:  startAssets,
				InitialBalances: initial,
				BaseFees:        baseFees,
				Seed:            seed,
				Scenario:        sc,
			})
			if err != nil {
				return nil, fmt.Errorf("scenario %s seed %d: %w", sc.Name, seed, err)
			}
			campaign.Results = append(campaign.Results, res)
			n++
			if progress != nil {
				progress(Progress{Done: n, Total: total, Scenario: sc.Name, Seed: seed})
			}
		}
	}
	return campaign, nil
}

// Markdown renders the report for every starting asset, in order.
func Markdown(c *backtest.Campaign, assets []string) string {
	var md strings.Builder
	for _, a := range assets {
		md.WriteString(c.Markdown(a))
		md.WriteString("\n")
	}
	return md.String()
}

// Flags collects the §80 verdict lines per starting asset.
func Flags(c *backtest.Campaign, assets []string) map[string][]string {
	out := make(map[string][]string, len(assets))
	for _, a := range assets {
		out[a] = c.Flags(a)
	}
	return out
}

// WriteFiles persists <base>.json (raw evidence) and <base>.md (report).
func WriteFiles(c *backtest.Campaign, assets []string, base string) (mdPath, jsonPath string, err error) {
	doc, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", "", err
	}
	jsonPath = base + ".json"
	if err := os.WriteFile(jsonPath, doc, 0o600); err != nil {
		return "", "", err
	}
	mdPath = base + ".md"
	if err := os.WriteFile(mdPath, []byte(Markdown(c, assets)), 0o600); err != nil {
		return "", "", err
	}
	return mdPath, jsonPath, nil
}
