package app

import (
	"log/slog"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/api"
	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/realtime"
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
		apiServer.Auth = buildAuth(cfg, log)
		hub := realtime.NewHub(256)
		apiServer.Hub = hub
		if engine != nil {
			apiServer.ScannerStatus = func() any { return engine.Status() }
			engine.Hub = hub
			if cfg.Mode == config.ModePaper {
				apiServer.Paper = paperProxy{engine}
			}
		}
		return append([]Component{apiServer}, others...)
	}
	return others
}

// paperProxy defers to the engine's paper controller, which exists only
// after metadata bootstrap; calls before readiness are safe no-ops with
// Running()=false.
type paperProxy struct{ e *Engine }

func (p paperProxy) Pause() {
	if pe := p.e.Paper(); pe != nil {
		pe.Pause()
	}
}

func (p paperProxy) Resume() {
	if pe := p.e.Paper(); pe != nil {
		pe.Resume()
	}
}

func (p paperProxy) Running() bool {
	if pe := p.e.Paper(); pe != nil {
		return pe.Running()
	}
	return false
}

// buildAuth wires the in-memory auth stores with the dev bootstrap admin.
// The storage layer (T-022) swaps in pgx-backed stores; until then a
// process restart clears sessions, which matches the CSRF key lifetime.
func buildAuth(cfg config.Bootstrap, log *slog.Logger) *auth.Manager {
	store := auth.NewMemoryStore()
	if cfg.AdminEmail != "" && cfg.AdminPassword != "" {
		hash, err := auth.HashPassword(cfg.AdminPassword)
		if err != nil {
			log.Error("bootstrap admin hash failed", "error", err)
		} else {
			store.AddUser(auth.User{
				ID: "admin-bootstrap", Email: cfg.AdminEmail,
				PasswordHash: hash, Role: auth.RoleAdmin,
			})
			log.Info("bootstrap admin configured", "email", cfg.AdminEmail)
		}
	} else {
		log.Warn("no bootstrap admin configured (ARB_ADMIN_EMAIL/ARB_ADMIN_PASSWORD); login unavailable until users exist")
	}
	return &auth.Manager{
		Users:    store,
		Sessions: store,
		Throttle: auth.NewThrottle(5, time.Minute, 10*time.Minute),
		TTL:      12 * time.Hour,
		Now:      time.Now,
	}
}
