// arbd runs the full platform: market data, scanner, paper engine, API,
// realtime hub, and (when configured) Telegram and the AI advisor.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cploutarchou/arb-chain-bot/internal/app"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	log := app.NewLogger(cfg.LogLevel)
	log.Info("arbd starting", "mode", string(cfg.Mode))

	a := app.New(cfg, log, app.BuildComponents(cfg, log, app.ProfileFull)...)
	if err := a.Run(context.Background()); err != nil {
		log.Error("arbd exited with error", "error", err)
		os.Exit(1)
	}
}
