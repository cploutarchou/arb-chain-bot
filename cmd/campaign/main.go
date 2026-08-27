// Command campaign runs the §80 profitability-validation campaign over
// one recording: every stress scenario × seed through internal/backtest,
// then the honest report (docs/deployment.md §3). The console's
// Campaigns page runs the very same internal/campaign code in-process;
// this binary is the headless path for a recordings volume.
//
//	campaign -recording <session-id> -dir /recordings/<session-id> \
//	  -assets USDT -balance USDT=10000 -seeds 1,2,3 -out /recordings/campaign-<id>
//
// The report's Verdict section carries the mandatory flag when the
// strategy is profitable only under perfect conditions.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/campaign"
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

	req := campaign.Request{
		Recording: *recording,
		Assets:    strings.Split(*assets, ","),
		Balances:  map[string]string{},
		MakerBps:  *makerBps,
		TakerBps:  *takerBps,
		Grid:      *gridFlag,
	}
	for _, kv := range strings.Split(*balances, ",") {
		parts := strings.SplitN(strings.TrimSpace(kv), "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("bad -balance entry %q (want ASSET=AMOUNT)", kv)
		}
		req.Balances[parts[0]] = parts[1]
	}
	for _, s := range strings.Split(*seedsFlag, ",") {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return fmt.Errorf("bad seed %q: %w", s, err)
		}
		req.Seeds = append(req.Seeds, n)
	}
	req, err := req.Normalize()
	if err != nil {
		return err
	}

	openCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	store, err := storage.Open(openCtx, *dsn)
	cancel()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	total := req.Total()
	c, err := campaign.Execute(ctx, store, segDir, req, func(p campaign.Progress) {
		fmt.Fprintf(os.Stderr, "[%d/%d] %s seed=%d done\n", p.Done, total, p.Scenario, p.Seed)
	})
	if err != nil {
		return err
	}
	mdPath, jsonPath, err := campaign.WriteFiles(c, req.Assets, *out)
	if err != nil {
		return err
	}
	fmt.Printf("campaign complete: %d runs → %s / %s\n", len(c.Results), mdPath, jsonPath)
	for _, a := range req.Assets {
		for _, f := range c.Flags(a) {
			fmt.Printf("VERDICT (%s): %s\n", a, f)
		}
	}
	return nil
}
