// replay runs the replay component profile of the platform.
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
	a := app.New(cfg, log, app.BuildComponents(cfg, log, app.Profile("replay"))...)
	if err := a.Run(context.Background()); err != nil {
		log.Error("replay exited with error", "error", err)
		os.Exit(1)
	}
}
