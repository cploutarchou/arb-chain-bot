// campaign runs the §80 profitability-validation campaign over one
// recorded market-data session: the full stress grid (or a chosen subset)
// × seeds, each run replaying the recording through the complete pipeline
// (books → scanner → risk → simulated execution → portfolio), then writes
// the honest report. It makes no network calls: recording segments come
// from disk, market metadata and the stream table from PostgreSQL.
//
// Typical use (after a RECORD deployment captured real feeds):
//
//	campaign -recording <session-id> -dir recordings/<session-id> \
//	         -assets USDT -balance USDT=10000 -seeds 1,2,3 -out campaign
//
// Exit code 1 means the run itself failed. A completed campaign always
// exits 0 — the VERDICT section of the report carries the §80 judgement,
// including the "profitable only under perfect conditions" flag.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/backtest"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange/binance"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "campaign:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		recording = flag.String("recording", "", "recording session id (market_recording_metadata.id)")
		dir       = flag.String("dir", "", "directory holding the recording's depth-*.seg.zst segments (default recordings/<recording>)")
		dsn       = flag.String("db", os.Getenv("ARB_DATABASE_URL"), "PostgreSQL DSN for market metadata + stream table (default $ARB_DATABASE_URL)")
		assets    = flag.String("assets", "USDT", "comma-separated starting assets")
		balances  = flag.String("balance", "USDT=10000", "comma-separated initial balances, ASSET=AMOUNT")
		seedsFlag = flag.String("seeds", "1,2,3", "comma-separated seeds per scenario")
		makerBps  = flag.Int64("fee-maker-bps", 10, "unstressed maker fee, bps")
		takerBps  = flag.Int64("fee-taker-bps", 10, "unstressed taker fee, bps")
		gridFlag  = flag.String("grid", "full", "scenario grid: full | baseline")
		out       = flag.String("out", "campaign", "output basename (writes <out>.md and <out>.json)")
	)
	flag.Parse()

	if *recording == "" {
		return fmt.Errorf("-recording is required")
	}
	if *dsn == "" {
		return fmt.Errorf("no database: set -db or ARB_DATABASE_URL (metadata and the stream table live there)")
	}
	segDir := *dir
	if segDir == "" {
		segDir = filepath.Join("recordings", *recording)
	}
	segments, err := filepath.Glob(filepath.Join(segDir, "depth-*.seg.zst"))
	if err != nil || len(segments) == 0 {
		return fmt.Errorf("no segments in %s (glob depth-*.seg.zst): %v", segDir, err)
	}
	sort.Strings(segments)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	store, err := storage.Open(ctx, *dsn)
	cancel()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer store.Close()

	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	streams, err := store.RecordingStreams(ctx, *recording)
	if err != nil {
		return err
	}
	allMarkets, err := store.LoadMarkets(ctx, binance.ID)
	if err != nil {
		return err
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
		return fmt.Errorf("only %d of the recorded symbols have market metadata; run the recorder with persistence so markets sync", len(markets))
	}

	var startAssets []exchange.Asset
	for _, a := range strings.Split(*assets, ",") {
		if a = strings.TrimSpace(a); a != "" {
			startAssets = append(startAssets, exchange.Asset(strings.ToUpper(a)))
		}
	}
	initial := map[exchange.Asset]decimal.Decimal{}
	for _, kv := range strings.Split(*balances, ",") {
		parts := strings.SplitN(strings.TrimSpace(kv), "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("bad -balance entry %q (want ASSET=AMOUNT)", kv)
		}
		amt, err := decimal.NewFromString(parts[1])
		if err != nil {
			return fmt.Errorf("bad balance amount %q: %w", parts[1], err)
		}
		initial[exchange.Asset(strings.ToUpper(parts[0]))] = amt
	}
	var seeds []int64
	for _, s := range strings.Split(*seedsFlag, ",") {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return fmt.Errorf("bad seed %q: %w", s, err)
		}
		seeds = append(seeds, n)
	}

	grid := backtest.DefaultGrid()
	if *gridFlag == "baseline" {
		grid = grid[:1]
	}
	baseFees := fees.Rate{
		Maker: decimal.NewFromInt(*makerBps).Div(decimal.NewFromInt(10_000)),
		Taker: decimal.NewFromInt(*takerBps).Div(decimal.NewFromInt(10_000)),
	}

	campaign := backtest.Campaign{Recording: *recording, GeneratedAt: time.Now().UTC()}
	total := len(grid) * len(seeds)
	n := 0
	for _, sc := range grid {
		for _, seed := range seeds {
			n++
			fmt.Fprintf(os.Stderr, "[%d/%d] %s seed=%d…\n", n, total, sc.Name, seed)
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
				return fmt.Errorf("scenario %s seed %d: %w", sc.Name, seed, err)
			}
			campaign.Results = append(campaign.Results, res)
		}
	}

	doc, err := json.MarshalIndent(campaign, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out+".json", doc, 0o600); err != nil {
		return err
	}
	var md strings.Builder
	for _, a := range startAssets {
		md.WriteString(campaign.Markdown(string(a)))
		md.WriteString("\n")
	}
	if err := os.WriteFile(*out+".md", []byte(md.String()), 0o600); err != nil {
		return err
	}
	fmt.Printf("campaign complete: %d runs over %d segments → %s.md / %s.json\n",
		len(campaign.Results), len(segments), *out, *out)
	for _, a := range startAssets {
		for _, f := range campaign.Flags(string(a)) {
			fmt.Printf("VERDICT (%s): %s\n", a, f)
		}
	}
	return nil
}
