// api runs only the HTTP API + realtime hub (console against
// recorded/replayed data, no live feeds).
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
	a := app.New(cfg, log, app.BuildComponents(cfg, log, app.ProfileAPI)...)
	if err := a.Run(context.Background()); err != nil {
		log.Error("api exited with error", "error", err)
		os.Exit(1)
	}
}
