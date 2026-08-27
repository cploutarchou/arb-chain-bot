package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/ai"
	"github.com/cploutarchou/arb-chain-bot/internal/api"
	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/campaign"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
	"github.com/cploutarchou/arb-chain-bot/internal/metrics"
	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/paper"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
	"github.com/cploutarchou/arb-chain-bot/internal/realtime"
	"github.com/cploutarchou/arb-chain-bot/internal/replay"
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
	var supervisor *Supervisor
	var botRunning atomic.Bool
	// bot/push/telegramAllow back GET /api/v1/telegram/status (BL-21);
	// nil (no token or empty allowlist) reports enabled:false honestly.
	var bot *telegram.Bot
	var push *telegram.PushSink
	var telegramAllow *allowSet

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
	platformSvc, err := buildPlatform(log, store, cfg)
	if err != nil {
		// P2-4: matches the storage.Open hard-failure shape above — a
		// document that failed to load must not silently become an
		// env-reseeded MemoryStore, discarding whatever was persisted.
		log.Error("platform settings load failed; refusing to start without a validated settings document", "error", err)
		return []Component{ComponentFunc{ComponentName: "platform-settings", Fn: func(context.Context) error { return err }}}
	}
	if store != nil {
		// API-profile fallback catalog; overridden below by engineCatalog
		// (preferred) when this profile actually runs an engine.
		platformSvc.Catalog = storage.Catalog{S: store}
	}

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
		// Rehydrate unresolved alerts so a restart does not blank the
		// console while incidents are still open (audit CR-P2-10).
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if active, err := store.Alerts().LoadActive(ctx, 500); err != nil {
			log.Error("alert rehydration failed", "error", err)
		} else if len(active) > 0 {
			center.LoadActive(active)
			log.Info("alert center rehydrated", "active", len(active))
		}
		cancel()
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
	var campaigns *campaign.Runner
	var replays *replay.Runner

	includeEngine := p == ProfileFull || p == ProfileScanner
	if includeEngine {
		engine = NewEngine(cfg, log)
		engine.Store = store
		engine.Strategy = stratSvc
		engine.Metrics = mtr
		engine.Notifier = notify
		engine.Center = center
		// The engine is no longer an app.Component in its own right: the
		// supervisor built below re-enters Engine.Run across restarts
		// (T-057 D4 — one stable *Engine value, never replaced). Every
		// existing seam that closes over `engine` (recorderProxy,
		// paperProxy, ScannerStatus, Reads, engine.Hub, AIInput,
		// telegramServices) keeps working unchanged.
		platformSvc.Catalog = engineCatalog{engine} // preferred over the storage fallback set above

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

		// §80 campaign runner: in-process replacement for `make campaign`;
		// needs persisted market metadata and the stream table.
		if store != nil {
			campaigns = &campaign.Runner{
				Sources: store, Store: store,
				Dir: cfg.RecordingDir, Log: log, NewID: newULID,
			}
			others = append(others, campaigns)

			// Console-driven replays (BL-17): one baseline backtest.Run
			// against a recording, optionally pinned to a persisted
			// strategy version — the console's "replay this recording
			// through the CURRENT (or a specific) strategy" button.
			replays = &replay.Runner{
				Sources: store, Strategy: stratSvc, Store: store,
				Dir: cfg.RecordingDir, Log: log, NewID: newULID,
			}
			others = append(others, replays)
		}

		// Telegram allowlist: one live set (T-057), fed by the platform
		// settings document — env is a first-boot seed only (D5). Both
		// Bot.Allowed and PushSink.Targets read the SAME set, so
		// revoking a user takes effect for commands and pushes together
		// (updating only one would leave a revoked user still receiving
		// alerts).
		allow := &allowSet{}
		allow.Set(platformSvc.Current().Settings.Telegram.Allowlist)
		platformSvc.Subscribe(func(snap platform.Snapshot) { allow.Set(snap.Settings.Telegram.Allowlist) })
		telegramAllow = allow

		// Telegram control surface: only with a token, a non-empty
		// allow-list, and an engine to control. The allowlist that
		// gates construction is the SETTINGS document's (v1 == Seed(cfg)
		// on a fresh install, so behavior matches today exactly); an
		// empty boot allowlist never builds the bot at all — the
		// honest caveat field_timing surfaces (design §1.3).
		if cfg.TelegramToken != "" && len(allow.IDs()) > 0 {
			client := telegram.NewClient("https://api.telegram.org/bot" + cfg.TelegramToken)
			bot = &telegram.Bot{
				Client:   client,
				Allowed:  allow.Allowed,
				Services: telegramServices{e: engine, n: notify, c: center, s: stratSvc, ai: aiSvc, rep: reportGen},
				Log:      log,
				Audit:    telegramAudit(log, store),
			}
			push = &telegram.PushSink{
				Client: client, Targets: allow.IDs,
				Log: log, OnDrop: notify.CountDrop,
			}
			notify.Register(push)
			others = append(others, bot, push)
			if mtr != nil {
				if err := mtr.RegisterTelegram(bot.Messages, bot.Errors); err != nil {
					log.Error("telegram metrics registration failed", "error", err)
				}
			}
			botRunning.Store(true)
			log.Info("telegram bot enabled", "allowlisted_users", len(allow.IDs()))
		} else if cfg.TelegramToken != "" {
			log.Warn("ARB_TELEGRAM_TOKEN set but the platform settings allowlist is empty; bot disabled (allow-list is mandatory)")
		}

		// Supervisor re-enters Engine.Run across a restart (T-057
		// §2.2-§2.6); it is what BuildComponents appends where it used
		// to append the engine directly.
		supervisor = &Supervisor{
			Engine:   engine,
			Settings: platformSvc,
			Recorder: engine.Recorder,
			Paper:    engine.Paper,
			Grace:    cfg.ShutdownGrace,
			Log:      log,
			Notify: func(sev notification.Severity, key, title, body string) {
				notify.Notify(notification.Event{Severity: sev, Key: key, Title: title, Body: body})
			},
			Audit:          sourceAudit(log, store, "system"),
			Strategy:       stratSvc,
			RunningWorkers: engine.RunningWorkers,
			Ready:          func() bool { return engine.Status().Ready },
			SessionID:      engine.SessionID,
			// P2-2: carries the pause decision into the engine's own
			// boot sequence rather than relying on a post-hoc re-Pause()
			// that has nothing to act on while s.Paper() is still nil
			// mid-bootstrap.
			SetPaperPaused: engine.SetPaperPaused,
		}
		if store != nil {
			supervisor.EndPaperSession = store.EndPaperSession
		}
		if campaigns != nil {
			supervisor.Campaigns = campaigns
		}
		if replays != nil {
			supervisor.Replays = replays
		}
		others = append(others, supervisor)
	}

	if p == ProfileFull || p == ProfileAPI {
		names := []string{"api"}
		for _, c := range others {
			names = append(names, c.Name())
		}
		info := api.BuildInfo{Components: names}
		apiServer := api.NewServer(cfg, log, info)
		authMgr, userAdmin := buildAuth(cfg, log, store)
		apiServer.Auth = authMgr
		apiServer.Users = userAdmin
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
		apiServer.Platform = platformSvc
		apiServer.PlatformCatalog = platformSvc.Catalog
		apiServer.BotRunning = func() bool { return botRunning.Load() }
		apiServer.Telegram = telegramStatusView(bot, push, telegramAllow, botRunning.Load)
		if engine != nil {
			apiServer.ScannerStatus = func() any { return engine.Status() }
			apiServer.Reads = NewReadModel(engine, stratSvc)
			apiServer.Triangles = NewTriangleReader(engine)
			engine.Hub = hub
			if cfg.Mode == config.ModePaper {
				apiServer.Paper = paperProxy{engine}
			}
			apiServer.Recorder = recorderProxy{engine}
		}
		if supervisor != nil {
			apiServer.Restart = restartProxy{supervisor}
			// "health" moves out of Engine.Run into the wiring (design
			// §2.6) so its snapshot can include supervisor state, which
			// the engine cannot see; "scanner"/"recordings" stay
			// engine-scoped (overwrite-on-restart is correct for them,
			// registered inside Engine.Run itself).
			healthSnapshot := func() map[string]any {
				data := map[string]any{"mode": string(cfg.Mode), "restart": supervisor.Status()}
				if engine != nil {
					data["engine"] = engine.Status()
				}
				return data
			}
			hub.RegisterTopic("health", func() (json.RawMessage, error) {
				return json.Marshal(healthSnapshot())
			})
			supervisor.OnState = func(RestartStatus) { _ = hub.Publish("health", healthSnapshot()) }
		}
		if campaigns != nil {
			hub.RegisterTopic("campaigns", func() (json.RawMessage, error) {
				runs, err := campaigns.List(context.Background(), 50)
				if err != nil {
					return nil, err
				}
				return json.Marshal(map[string]any{"runs": runs})
			})
			campaigns.Publish = func(data any) { _ = hub.Publish("campaigns", data) }
			apiServer.Campaigns = campaigns
		}
		if replays != nil {
			hub.RegisterTopic("replays", func() (json.RawMessage, error) {
				runs, err := replays.List(context.Background(), 50)
				if err != nil {
					return nil, err
				}
				return json.Marshal(map[string]any{"runs": runs})
			})
			replays.Publish = func(data any) { _ = hub.Publish("replays", data) }
			apiServer.Replays = replays
		}
		return append([]Component{apiServer}, others...)
	}
	return others
}

// recorderProxy defers to the engine's recording control, which exists
// only after metadata bootstrap; calls before readiness fail loudly
// rather than start a session nobody tracks.
type recorderProxy struct{ e *Engine }

var errRecorderNotReady = errors.New("recorder: engine not bootstrapped yet")

func (p recorderProxy) StartSession() (string, error) {
	if c := p.e.Recorder(); c != nil {
		return c.StartSession()
	}
	return "", errRecorderNotReady
}

func (p recorderProxy) Stop() (string, error) {
	if c := p.e.Recorder(); c != nil {
		return c.Stop()
	}
	return "", marketdata.ErrRecorderIdle
}

func (p recorderProxy) Status() marketdata.RecorderStatus {
	if c := p.e.Recorder(); c != nil {
		return c.Status()
	}
	return marketdata.RecorderStatus{}
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

// Reset delegates to Engine.ResetPaper (BL-10), translating the paper
// package's own "not idle" sentinel into the API layer's at this
// boundary — the api package stays free of a direct dependency on
// internal/paper.
func (p paperProxy) Reset(ctx context.Context) error {
	err := p.e.ResetPaper(ctx)
	if errors.Is(err, paper.ErrActive) {
		return api.ErrPaperNotIdle
	}
	return err
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

// telegramStatusView builds the func api.Server.Telegram reads on every
// GET /api/v1/telegram/status request (BL-21). bot/push/allow may each
// be nil (no token configured, or an empty allowlist held the bot back
// per field_timing's caveat); the view reports that honestly rather
// than 404ing — "not configured" is itself the answer. The token is
// never read here.
func telegramStatusView(bot *telegram.Bot, push *telegram.PushSink, allow *allowSet, enabled func() bool) func() api.TelegramStatusView {
	return func() api.TelegramStatusView {
		view := api.TelegramStatusView{Enabled: enabled()}
		if allow != nil {
			ids := allow.IDs()
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			view.Allowlist = ids
		}
		if bot != nil {
			st := bot.Status()
			view.BotUsername = st.BotUsername
			view.Messages, view.Errors = st.Messages, st.Errors
			view.LastPollOK, view.LastPollError = st.LastPollOK, st.LastPollError
			view.LastGetMeOK, view.LastGetMeError = st.LastGetMeOK, st.LastGetMeError
			if !st.LastPollAt.IsZero() {
				view.LastPollAt = &st.LastPollAt
			}
			if !st.LastGetMeAt.IsZero() {
				view.LastGetMeAt = &st.LastGetMeAt
			}
		}
		if push != nil {
			st := push.Status()
			view.PushesSent, view.PushErrors = st.Pushed, st.Errors
			if !st.LastPushAt.IsZero() {
				view.LastPushedAt = &st.LastPushAt
			}
		}
		return view
	}
}

// allowSet is the one live Telegram allowlist both telegram.Bot.Allowed
// and telegram.PushSink.Targets read (T-057 §1.5): updating only one of
// the two copies the pre-T-057 wiring kept would leave a revoked user
// still receiving pushes.
type allowSet struct {
	p atomic.Pointer[map[int64]bool]
}

func (a *allowSet) Set(ids []int64) {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	a.p.Store(&m)
}

func (a *allowSet) Allowed(id int64) bool {
	if m := a.p.Load(); m != nil {
		return (*m)[id]
	}
	return false
}

func (a *allowSet) IDs() []int64 {
	m := a.p.Load()
	if m == nil {
		return nil
	}
	out := make([]int64, 0, len(*m))
	for id := range *m {
		out = append(out, id)
	}
	return out
}

// restartProxy adapts *Supervisor onto api.RestartController, classifying
// Supervisor.Request's sentinel errors into the design §2.5 refusal
// codes so internal/api never needs to import internal/app (which
// already imports internal/api to build the Server — a cycle).
type restartProxy struct{ sup *Supervisor }

func (p restartProxy) Request(actor, reason string, stopRecording bool) api.RestartRequestResult {
	err := p.sup.Request(RestartRequest{Actor: actor, Reason: reason, StopRecording: stopRecording})
	if err == nil {
		return api.RestartRequestResult{Accepted: true}
	}
	code := "restart_failed"
	switch {
	case errors.Is(err, ErrCampaignRunning):
		code = "campaign_running"
	case errors.Is(err, ErrReplayRunning):
		code = "replay_running"
	case errors.Is(err, ErrRecordingActive):
		code = "recording_active"
	case errors.Is(err, ErrRestartInProgress):
		code = "restart_in_progress"
	}
	return api.RestartRequestResult{Accepted: false, Code: code, Message: err.Error()}
}

func (p restartProxy) Status() api.RestartStatus {
	st := p.sup.Status()
	return api.RestartStatus{
		State:           string(st.State),
		SettingsVersion: st.SettingsVersion,
		PendingVersion:  st.PendingVersion,
		RequestedBy:     st.RequestedBy,
		RequestedAt:     st.RequestedAt,
		ReadyAt:         st.ReadyAt,
		Restarts:        st.Restarts,
		PendingReasons:  st.PendingReasons,
		Error:           st.Error,
	}
}

// webAudit carries the caller's IP and correlation ID into the audit row
// (audit S-008) — web is the surface where those forensics exist.
func webAudit(log *slog.Logger, store *storage.Store) func(actor, action, entity, ip, correlationID string) {
	return func(actor, action, entity, ip, correlationID string) {
		if store == nil {
			log.Info("audit event (memory-only)", "actor", actor, "action", action,
				"entity", entity, "source", "web", "ip", ip, "correlation_id", correlationID)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.InsertAuditEvent(ctx, storage.AuditRow{
			ID: newULID(), Actor: actor, Source: "web",
			Action: action, Entity: entity, IP: ip, CorrelationID: correlationID,
		}); err != nil {
			log.Error("audit insert failed", "source", "web", "error", err)
		}
	}
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

// buildPlatform wires the versioned platform-settings service (T-057):
// DB-backed rows when persistence is enabled, in-memory otherwise. Load
// seeds version 1 from platform.Seed(cfg) when the table is empty, and
// logs when it is not — the env symbol/asset/balance/allowlist
// variables are first-boot seeds only from then on (D5).
//
// P2-4: a Load failure against a configured database used to be
// swallowed — silently swapping in a fresh MemoryStore re-seeded from
// the environment, discarding whatever was actually persisted (wrong
// venues/symbols/fees/balances, and any operator writes since the
// database became unreachable, would be lost with no trace beyond a log
// line). That is exactly the "silently running without the persistence
// the operator asked for would be a lie" policy this file already
// enforces for storage.Open failing outright (see the store == nil
// branch above); Load failing must fail the SAME way, not differently
// just because it happens one step later.
func buildPlatform(log *slog.Logger, store *storage.Store, cfg config.Bootstrap) (*platform.Service, error) {
	var st platform.Store
	var audit func(context.Context, platform.AuditEvent)
	if store != nil {
		st = store.PlatformSettings()
		audit = func(ctx context.Context, ev platform.AuditEvent) {
			row := storage.AuditRow{
				ID: newULID(), Actor: ev.Actor, Source: ev.Source,
				Action: ev.Action, Entity: ev.Entity, EntityID: ev.EntityID,
				Before: ev.Before, After: ev.After,
			}
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := store.InsertAuditEvent(ctx, row); err != nil {
				log.Error("platform settings audit insert failed", "action", ev.Action, "error", err)
			}
		}
	} else {
		st = platform.NewMemoryStore()
	}
	return newPlatformService(log, st, audit, cfg)
}

// newPlatformService is buildPlatform's storage-agnostic core, split out
// so a Store.Load failure (P2-4) can be unit tested against a fake
// platform.Store instead of requiring a real, deliberately-broken
// database.
func newPlatformService(log *slog.Logger, st platform.Store, audit func(context.Context, platform.AuditEvent), cfg config.Bootstrap) (*platform.Service, error) {
	svc := platform.NewService(st, log, audit)
	svc.Mode = cfg.Mode
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := svc.Load(ctx, cfg); err != nil {
		return nil, fmt.Errorf("app: platform settings load failed: %w", err)
	}
	return svc, nil
}

// buildAuth wires auth stores: pgx-backed when persistence is enabled
// (sessions survive restarts), in-memory otherwise. The bootstrap admin
// from the environment is upserted either way (dev convenience;
// production users are managed through the console). It also returns
// the users & roles admin service (BL-11) over the same backing store,
// so console user management works identically with or without a
// database.
func buildAuth(cfg config.Bootstrap, log *slog.Logger, store *storage.Store) (*auth.Manager, *auth.AdminService) {
	var users auth.UserStore
	var sessions auth.SessionStore
	var admin auth.AdminStore

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
		u := auth.User{ID: "admin-bootstrap", Email: cfg.AdminEmail, PasswordHash: hash, Role: auth.RoleAdmin, CreatedAt: time.Now().UTC()}
		if err := add(u); err != nil {
			log.Error("bootstrap admin store failed", "error", err)
			return
		}
		log.Info("bootstrap admin configured", "email", cfg.AdminEmail)
	}

	if store != nil {
		as := store.Auth()
		users, sessions, admin = as, as, as
		bootstrapAdmin(func(u auth.User) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return as.UpsertUser(ctx, u)
		})
	} else {
		mem := auth.NewMemoryStore()
		users, sessions, admin = mem, mem, mem
		bootstrapAdmin(func(u auth.User) error { mem.AddUser(u); return nil })
	}
	mgr := &auth.Manager{
		Users:    users,
		Sessions: sessions,
		Throttle: auth.NewThrottle(5, time.Minute, 10*time.Minute),
		// Wider per-IP net so rotating emails cannot evade the account
		// throttle (audit S-006).
		IPThrottle: auth.NewThrottle(20, time.Minute, 10*time.Minute),
		TTL:        12 * time.Hour,
		Now:        time.Now,
	}
	adminSvc := &auth.AdminService{
		Store: admin, Sessions: sessions, Now: time.Now, IDGen: newULID,
		// P3-13: self-service password change had no throttle at all,
		// unlike login — a stolen session cookie could otherwise
		// brute-force the current password with no rate limit.
		PasswordThrottle: auth.NewThrottle(5, time.Minute, 10*time.Minute),
	}
	return mgr, adminSvc
}
