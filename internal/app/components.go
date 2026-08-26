package app

import (
	"context"
	"log/slog"
	"time"

	"encoding/json"

	"github.com/cploutarchou/arb-chain-bot/internal/ai"
	"github.com/cploutarchou/arb-chain-bot/internal/api"
	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/metrics"
	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/realtime"
	"github.com/cploutarchou/arb-chain-bot/internal/reporting"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
	"github.com/cploutarchou/arb-chain-bot/internal/telegram"
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

	stratSvc := buildStrategy(log, store)

	// Metrics are always built (near-zero idle cost); a registration
	// failure logs and the platform runs unobserved rather than not at all.
	mtr, err := metrics.New()
	if err != nil {
		log.Error("metrics init failed; continuing without metrics", "error", err)
		mtr = nil
	}
	if mtr != nil && cfg.MetricsAddr != "" {
		others = append(others, &metrics.Server{Addr: cfg.MetricsAddr, Handler: mtr.Handler, Log: log})
	}

	// One notification router for every channel; routing config follows
	// the versioned strategy config (hot swap included).
	notify := notification.NewService(log, notificationConfig(stratSvc.Current()), time.Now)
	stratSvc.Subscribe(func(snap strategy.Snapshot) {
		notify.Reconfigure(notificationConfig(snap))
	})

	// The alert center records every delivery (routing controls channels,
	// never whether an alert exists) and owns the shared lifecycle.
	center := &notification.Center{Log: log, IDGen: newULID, Now: time.Now}
	if store != nil {
		center.Store = store.Alerts()
	}
	notify.RegisterAlways(center)

	// AI advisor: provider per config. It is an analyst off the hot
	// path; absence just leaves the routes and commands reporting so.
	var aiSvc *ai.Service
	if adv := buildAdvisor(cfg, log); adv != nil {
		aiSvc = &ai.Service{
			Advisor: adv, Strategy: stratSvc,
			Notify: notify.Notify, Log: log, IDGen: newULID,
		}
		if store != nil {
			aiSvc.Store = store.AI()
		}
		if mtr != nil {
			if err := mtr.RegisterAI(aiSvc.Requests, aiSvc.Failures); err != nil {
				log.Error("ai metrics registration failed", "error", err)
			}
		}
		log.Info("ai advisor enabled", "provider", adv.Name(), "model", adv.Model())
	}

	var reportGen *reporting.Generator

	includeEngine := p == ProfileFull || p == ProfileScanner
	if includeEngine {
		engine = NewEngine(cfg, log)
		engine.Store = store
		engine.Strategy = stratSvc
		engine.Metrics = mtr
		engine.Notifier = notify
		others = append(others, engine)

		if aiSvc != nil {
			others = append(others, &ai.Scheduler{
				Service:  aiSvc,
				InputFor: engine.AIInput,
				Log:      log,
			})
		}

		// Daily/weekly reports: detailed version persisted (when the DB
		// is on), concise digest through the notification router.
		var history reporting.HistorySource
		if store != nil {
			history = store.Reports()
		}
		reportGen = &reporting.Generator{
			Sources: reportSources(engine, stratSvc, center, aiSvc, history),
			Notify:  notify.Notify,
			Log:     log, IDGen: newULID, Now: time.Now,
		}
		others = append(others, &reporting.Scheduler{Generator: reportGen, Log: log})

		// Telegram control surface: only with a token, a non-empty
		// allow-list, and an engine to control.
		if cfg.TelegramToken != "" && len(cfg.TelegramAllowlist) > 0 {
			allow := make(map[int64]bool, len(cfg.TelegramAllowlist))
			for _, id := range cfg.TelegramAllowlist {
				allow[id] = true
			}
			client := telegram.NewClient("https://api.telegram.org/bot" + cfg.TelegramToken)
			bot := &telegram.Bot{
				Client:    client,
				Allowlist: allow,
				Services:  telegramServices{e: engine, n: notify, c: center, s: stratSvc, ai: aiSvc, rep: reportGen},
				Log:       log,
				Audit:     telegramAudit(log, store),
			}
			push := &telegram.PushSink{
				Client: client, ChatIDs: cfg.TelegramAllowlist,
				Log: log, OnDrop: notify.CountDrop,
			}
			notify.Register(push)
			others = append(others, bot, push)
			if mtr != nil {
				if err := mtr.RegisterTelegram(bot.Messages, bot.Errors); err != nil {
					log.Error("telegram metrics registration failed", "error", err)
				}
			}
			log.Info("telegram bot enabled", "allowlisted_users", len(cfg.TelegramAllowlist))
		} else if cfg.TelegramToken != "" {
			log.Warn("ARB_TELEGRAM_TOKEN set but ARB_TELEGRAM_ALLOWLIST empty; bot disabled (allow-list is mandatory)")
		}
	}

	if p == ProfileFull || p == ProfileAPI {
		names := []string{"api"}
		for _, c := range others {
			names = append(names, c.Name())
		}
		info := api.BuildInfo{Components: names}
		apiServer := api.NewServer(cfg, log, info)
		apiServer.Auth = buildAuth(cfg, log, store)
		apiServer.Strategy = stratSvc
		hub := realtime.NewHub(256)
		apiServer.Hub = hub
		if mtr != nil {
			if cfg.MetricsAddr == "" {
				apiServer.MetricsHandler = mtr.Handler
			}
			apiServer.ObserveRequest = mtr.ObserveAPIRequest
			if err := mtr.RegisterHub(func() int64 { return int64(hub.Clients()) }); err != nil {
				log.Error("hub metrics registration failed", "error", err)
			}
		}
		// Web alert channel: the topic snapshot is the center's live list;
		// deliveries and lifecycle changes stream as events.
		hub.RegisterTopic("alerts", func() (json.RawMessage, error) {
			return json.Marshal(map[string]any{
				"alerts": center.List("", 50),
				"active": center.ActiveCount(),
			})
		})
		notify.Register(webSink{hub: hub})
		center.OnChange = func(a notification.Alert) {
			_ = hub.Publish("alerts", map[string]any{"kind": "alert_change", "alert": a})
		}
		apiServer.Alerts = center
		apiServer.AI = aiSvc
		apiServer.AuditAction = webAudit(log, store)
		apiServer.Store = store
		apiServer.Reports = reportGen
		if engine != nil {
			apiServer.ScannerStatus = func() any { return engine.Status() }
			apiServer.Reads = NewReadModel(engine, stratSvc)
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

// buildAdvisor selects the AI provider. Keys come from the environment
// only and never leave the process except in the provider's auth header.
func buildAdvisor(cfg config.Bootstrap, log *slog.Logger) ai.Advisor {
	switch cfg.AIProvider {
	case "fake":
		return ai.Fake{}
	case "", "anthropic":
		if cfg.AnthropicAPIKey == "" {
			if cfg.AIProvider == "anthropic" {
				log.Warn("ARB_AI_PROVIDER=anthropic but ANTHROPIC_API_KEY unset; advisor disabled")
			}
			return nil
		}
		return ai.NewAnthropic(cfg.AnthropicAPIKey, cfg.AIModel)
	default:
		log.Warn("unknown ARB_AI_PROVIDER; advisor disabled", "provider", cfg.AIProvider)
		return nil
	}
}

// notificationConfig converts the strategy payload's notification slice.
func notificationConfig(snap strategy.Snapshot) notification.Config {
	return notification.Config{
		Cooldown: time.Duration(snap.Params.Notifications.CooldownSeconds) * time.Second,
		Routes:   snap.Params.Notifications.Routes,
	}
}

// webSink publishes deliveries onto the realtime hub's alerts topic.
type webSink struct{ hub *realtime.Hub }

func (w webSink) Name() string { return "web" }

func (w webSink) Deliver(d notification.Delivery) {
	_ = w.hub.Publish("alerts", map[string]any{
		"severity":   d.Severity.String(),
		"key":        d.Key,
		"title":      d.Title,
		"body":       d.Body,
		"at":         d.At,
		"suppressed": d.Suppressed,
	})
}

// sourceAudit records control actions into audit_events when
// persistence is on; log-only otherwise.
func sourceAudit(log *slog.Logger, store *storage.Store, source string) func(actor, action, entity string) {
	return func(actor, action, entity string) {
		if store == nil {
			log.Info("audit event (memory-only)", "actor", actor, "action", action, "entity", entity, "source", source)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.InsertAuditEvent(ctx, storage.AuditRow{
			ID: newULID(), Actor: actor, Source: source,
			Action: action, Entity: entity,
		}); err != nil {
			log.Error("audit insert failed", "source", source, "error", err)
		}
	}
}

func telegramAudit(log *slog.Logger, store *storage.Store) func(actor, action, entity string) {
	return sourceAudit(log, store, "telegram")
}

func webAudit(log *slog.Logger, store *storage.Store) func(actor, action, entity string) {
	return sourceAudit(log, store, "web")
}

// buildStrategy wires the versioned config service: DB-backed rows when
// persistence is enabled (versions survive restarts), in-memory
// otherwise. A failed load is a hard failure surfaced by a service that
// starts empty — but since Load also seeds defaults, failure here means
// the database rejected the seed, which the log records; the process
// still runs on validated in-memory defaults rather than nothing.
func buildStrategy(log *slog.Logger, store *storage.Store) *strategy.Service {
	var st strategy.Store
	var audit func(context.Context, strategy.AuditEvent)
	if store != nil {
		st = store.StrategyConfigs()
		audit = func(ctx context.Context, ev strategy.AuditEvent) {
			row := storage.AuditRow{
				ID: newULID(), Actor: ev.Actor, Source: ev.Source,
				Action: ev.Action, Entity: ev.Entity, EntityID: ev.EntityID,
				Before: ev.Before, After: ev.After,
			}
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := store.InsertAuditEvent(ctx, row); err != nil {
				log.Error("audit event insert failed", "action", ev.Action, "error", err)
			}
		}
	} else {
		st = strategy.NewMemoryStore()
	}
	svc := strategy.NewService(st, log, audit)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := svc.Load(ctx); err != nil {
		log.Error("strategy config load failed; falling back to in-memory defaults", "error", err)
		svc = strategy.NewService(strategy.NewMemoryStore(), log, audit)
		if _, err := svc.Load(context.Background()); err != nil {
			log.Error("in-memory strategy seed failed", "error", err)
		}
	}
	return svc
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
