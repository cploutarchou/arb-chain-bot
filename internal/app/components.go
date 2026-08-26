package app

import (
	"log/slog"

	"github.com/cploutarchou/arb-chain-bot/internal/api"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
)

// Profile selects which component set a cmd/ entry point runs. All
// profiles share the same wiring; a profile only narrows it.
type Profile string

const (
	ProfileFull     Profile = "full"
	ProfileAPI      Profile = "api"
	ProfileScanner  Profile = "scanner"
	ProfileRecorder Profile = "recorder"
	ProfileReplay   Profile = "replay"
	ProfileWorker   Profile = "worker"
)

// BuildComponents assembles the component set for a profile.
//
// Implementation status is explicit: components appear here only once they
// are actually implemented (docs/MASTER_PLAN.md tracks the rest). The API
// server's status payload reports which components this build includes so
// the process never pretends to run subsystems that do not exist yet.
func BuildComponents(cfg config.Bootstrap, log *slog.Logger, p Profile) []Component {
	// Recorder, replay, worker, and Telegram components join this list as
	// their MASTER_PLAN tasks complete (T-032, T-033).
	var others []Component
	var engine *Engine

	includeEngine := p == ProfileFull || p == ProfileScanner
	if includeEngine {
		engine = NewEngine(cfg, log)
		others = append(others, engine)
	}

	if p == ProfileFull || p == ProfileAPI {
		names := []string{"api"}
		for _, c := range others {
			names = append(names, c.Name())
		}
		info := api.BuildInfo{Components: names}
		apiServer := api.NewServer(cfg, log, info)
		if engine != nil {
			apiServer.ScannerStatus = func() any { return engine.Status() }
		}
		return append([]Component{apiServer}, others...)
	}
	return others
}
