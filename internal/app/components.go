package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/api"
	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/realtime"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
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

	// Persistence is optional in dev (empty DSN = in-memory only). A
	// configured-but-unreachable database is a hard failure at boot:
	// silently running without the persistence the operator asked for
	// would be a lie.
	var store *storage.Store
	if cfg.DatabaseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		s, err := storage.Open(ctx, cfg.DatabaseURL)
		cancel()
		if err != nil {
			log.Error("database configured but unreachable; refusing to start without persistence", "error", err)
			return []Component{ComponentFunc{ComponentName: "storage", Fn: func(context.Context) error { return err }}}
		}
		store = s
		log.Info("persistence enabled")
	} else {
		log.Warn("ARB_DATABASE_URL unset; running without persistence (sessions and history are memory-only)")
	}

	includeEngine := p == ProfileFull || p == ProfileScanner
	if includeEngine {
		engine = NewEngine(cfg, log)
		engine.Store = store
		others = append(others, engine)
	}

	if p == ProfileFull || p == ProfileAPI {
		names := []string{"api"}
		for _, c := range others {
			names = append(names, c.Name())
		}
		info := api.BuildInfo{Components: names}
		apiServer := api.NewServer(cfg, log, info)
		apiServer.Auth = buildAuth(cfg, log, store)
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

// buildAuth wires auth stores: pgx-backed when persistence is enabled
// (sessions survive restarts), in-memory otherwise. The bootstrap admin
// from the environment is upserted either way (dev convenience;
// production users are managed through the console).
func buildAuth(cfg config.Bootstrap, log *slog.Logger, store *storage.Store) *auth.Manager {
	var users auth.UserStore
	var sessions auth.SessionStore

	bootstrapAdmin := func(add func(auth.User) error) {
		if cfg.AdminEmail == "" || cfg.AdminPassword == "" {
			log.Warn("no bootstrap admin configured (ARB_ADMIN_EMAIL/ARB_ADMIN_PASSWORD); login unavailable until users exist")
			return
		}
		hash, err := auth.HashPassword(cfg.AdminPassword)
		if err != nil {
			log.Error("bootstrap admin hash failed", "error", err)
			return
		}
		u := auth.User{ID: "admin-bootstrap", Email: cfg.AdminEmail, PasswordHash: hash, Role: auth.RoleAdmin}
		if err := add(u); err != nil {
			log.Error("bootstrap admin store failed", "error", err)
			return
		}
		log.Info("bootstrap admin configured", "email", cfg.AdminEmail)
	}

	if store != nil {
		as := store.Auth()
		users, sessions = as, as
		bootstrapAdmin(func(u auth.User) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return as.UpsertUser(ctx, u)
		})
	} else {
		mem := auth.NewMemoryStore()
		users, sessions = mem, mem
		bootstrapAdmin(func(u auth.User) error { mem.AddUser(u); return nil })
	}
	return &auth.Manager{
		Users:    users,
		Sessions: sessions,
		Throttle: auth.NewThrottle(5, time.Minute, 10*time.Minute),
		TTL:      12 * time.Hour,
		Now:      time.Now,
	}
}
