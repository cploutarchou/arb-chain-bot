// arbd runs the full platform: market data, scanner, paper engine, API,
// realtime hub, and (when configured) Telegram and the AI advisor.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cploutarchou/arb-chain-bot/internal/app"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/tracing"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	log := app.NewLogger(cfg.LogLevel)
	log.Info("arbd starting", "mode", string(cfg.Mode))

	// O11: stage-boundary spans over OTLP when OTEL_EXPORTER_OTLP_ENDPOINT
	// is set; noop (zero cost) otherwise. Shutdown flushes on exit.
	shutdownTracing, err := tracing.Init(context.Background(), log)
	if err != nil {
		log.Error("tracing init failed; continuing without spans", "error", err)
		shutdownTracing = func(context.Context) error { return nil }
	}

	a := app.New(cfg, log, app.BuildComponents(cfg, log, app.ProfileFull)...)
	err = a.Run(context.Background())
	if flushErr := shutdownTracing(context.Background()); flushErr != nil {
		log.Error("tracing shutdown failed", "error", flushErr)
	}
	if err != nil {
		log.Error("arbd exited with error", "error", err)
		os.Exit(1)
	}
}
