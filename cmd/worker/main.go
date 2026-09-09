// worker runs the worker component profile of the platform: whatever
// internal/app.BuildComponents wires for app.Profile("worker"), plus
// retention (docs/audit/database-audit.md D5,
// docs/audit/infra-delivery-audit.md I9 — this binary built no components
// of its own before this). Retention is wired here rather than inside
// BuildComponents because it needs its own database handle: every other
// cmd/ entry must stay free of its DELETE traffic, and cmd/worker is the
// only binary that should ever run it.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/app"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	log := app.NewLogger(cfg.LogLevel)
	components := app.BuildComponents(cfg, log, app.Profile("worker"))

	// Persistence is optional in dev (empty ARB_DATABASE_URL), same
	// convention as internal/app.BuildComponents: with nothing to prune,
	// retention simply does not run rather than failing the process.
	if cfg.DatabaseURL == "" {
		log.Warn("worker: ARB_DATABASE_URL not set; retention disabled (nothing to prune)")
	} else {
		rc, err := config.LoadRetention()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		openCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		store, err := storage.Open(openCtx, cfg.DatabaseURL)
		cancel()
		if err != nil {
			// Matches internal/app.BuildComponents: a configured-but-
			// unreachable database is a hard failure at boot, not a silent
			// "retention just doesn't run".
			log.Error("worker: database unreachable", "error", err)
			os.Exit(1)
		}
		defer store.Close()

		job := &storage.RetentionScheduler{
			Runner: store,
			Log:    log,
			Windows: map[string]time.Duration{
				"opportunities_qualified": rc.OpportunitiesQualified,
				"opportunities_rejected":  rc.OpportunitiesRejected,
				"exchange_health":         rc.ExchangeHealth,
				"system_events":           rc.SystemEvents,
				"funding_history":         rc.FundingHistory,
				"sessions":                rc.Sessions,
				"recording_metadata":      rc.RecordingMetadata,
			},
			RunAtHour:        rc.RunAtHour,
			RunAtMinute:      rc.RunAtMinute,
			BatchSize:        rc.BatchSize,
			BatchSleep:       rc.BatchSleep,
			StatementTimeout: rc.StatementTimeout,
			DryRun:           rc.DryRun,
		}
		components = append(components, job)
		log.Info("worker: retention configured",
			"run_at_utc", fmt.Sprintf("%02d:%02d", rc.RunAtHour, rc.RunAtMinute),
			"dry_run", rc.DryRun, "batch_size", rc.BatchSize,
			"opportunities_qualified", rc.OpportunitiesQualified.String(),
			"opportunities_rejected", rc.OpportunitiesRejected.String(),
			"exchange_health", rc.ExchangeHealth.String(),
			"system_events", rc.SystemEvents.String(),
			"funding_history", rc.FundingHistory.String(),
			"sessions", rc.Sessions.String(),
			"recording_metadata", rc.RecordingMetadata.String())
	}

	a := app.New(cfg, log, components...)
	if err := a.Run(context.Background()); err != nil {
		log.Error("worker exited with error", "error", err)
		os.Exit(1)
	}
}
