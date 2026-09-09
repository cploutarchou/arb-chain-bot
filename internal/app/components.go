package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
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
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/alerts"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/report"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/venue"
	"github.com/cploutarchou/arb-chain-bot/internal/secrets"
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
	var telegramReason atomic.Value // string: why the bot is not running (T-059 §4.2)
	telegramReason.Store("")

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
		// db_migrations_pending: compared once at boot against the
		// version this binary was built for (storage.LatestMigrationVersion).
		bctx, bcancel := context.WithTimeout(context.Background(), 5*time.Second)
		if pending, err := s.MigrationsPending(bctx); err != nil {
			log.Error("schema_migrations check failed; reporting migrations as pending", "error", err)
			migrationsPending.Store(1)
		} else {
			migrationsPending.Store(pending)
			if pending > 0 {
				log.Warn("schema migrations pending: run `make migrate`", "built_for", storage.LatestMigrationVersion)
			}
		}
		bcancel()
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

	// screener.Service (T-067/T-068, docs/design/scanner-suite.md §2):
	// the Scanner Suite backend core. Built unconditionally (like
	// platformSvc above) so every profile can serve the read routes;
	// only the API profile actually attaches it to a Server below and
	// runs the T-066 venue collectors (next block).
	screenerSvc, err := buildScreener(log, store)
	if err != nil {
		log.Error("screener settings load failed; refusing to start without a validated settings document", "error", err)
		return []Component{ComponentFunc{ComponentName: "screener-settings", Fn: func(context.Context) error { return err }}}
	}
	// T-066: venue collectors (public REST only) run as one component in
	// the profiles that serve the screener API; they start with the
	// process and stop when its context ends. Other profiles keep the
	// honest "not_started" (no runner wired).
	if p == ProfileFull || p == ProfileAPI {
		screenerSvc.Collectors = &venue.Poller{
			Book: screenerSvc.Book, Log: log, Funding: screenerSvc.Funding,
			Current: func() screener.Settings { return screenerSvc.Current().Settings },
		}
		others = append(others, ComponentFunc{ComponentName: "screener-collectors", Fn: func(ctx context.Context) error {
			if err := screenerSvc.StartCollectors(ctx); err != nil {
				return err
			}
			<-ctx.Done()
			screenerSvc.StopCollectors()
			return nil
		}})
	}

	// platform.log_level is hot (T-059 D6): the process-wide LevelVar
	// follows the document from here on; ARB_LOG_LEVEL seeded v1 only.
	platformSvc.Subscribe(func(snap platform.Snapshot) {
		if err := SetLogLevel(snap.Settings.Platform.LogLevel); err != nil {
			log.Warn("platform.log_level not applied", "error", err)
		}
	})

	// Secrets vault (T-060): vault-first, env-fallback resolution for the
	// two registry entries. A missing/invalid ARB_SECRET_KEY closes the
	// vault; the platform keeps running on env-provided secrets.
	secretsMgr := buildSecrets(log, store, cfg)
	// Tenancy + entitlements + billing (T-081..T-083): pgx-backed when
	// persistence is on; without a database every account acts in the
	// platform organisation and billing is unconfigured.
	tenant := buildTenancy(log, store, secretsMgr, cfg)

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
	if mtr != nil && store != nil {
		if err := mtr.RegisterMigrations(migrationsPending.Load); err != nil {
			log.Error("migrations metric registration failed", "error", err)
		}
	}

	// One notification router for every channel; routing config follows
	// the versioned strategy config (hot swap included).
	notify := notification.NewService(log, notificationConfig(stratSvc.Current()), time.Now)
	stratSvc.Subscribe(func(snap strategy.Snapshot) {
		notify.Reconfigure(notificationConfig(snap))
	})

	// T-070/T-071: the alert evaluator and the automatic PAPER executor
	// run on the screener poll interval in the same profiles as the
	// collectors. Executor first (funding accrual, exits, realised-slip
	// measurement of the previous poll), then the evaluator, whose
	// opened events call the executor synchronously. The executor's
	// ledger is migration 000011 (screener_paper_*) when persistence is
	// on, in-memory otherwise; it never touches paper_cycles nor places
	// a real order (execution.LiveExecutor stays disabled).
	var screenerReports *report.Generator
	// feedRateLimited feeds exchange_rate_limited_total{venue="binance"}
	// from the engine's Binance feed once the engine exists (below).
	var feedRateLimited func() int64
	if p == ProfileFull || p == ProfileAPI {
		var ledger paperexec.Ledger = paperexec.NewMemoryLedger()
		if store != nil {
			ledger = store.ScreenerPaper()
		}
		evaluator := alerts.New(screenerSvc, notify.Notify, log)
		// T-082: alerts.per_day + alerts.channels are enforced at open
		// time per the rule's organisation (nil without tenancy).
		evaluator.SetEntitle(tenant.alertEntitle())
		// T-086: e-mail (SMTP, secrets registry "smtp_url", read fresh on
		// every send so a vault write applies immediately) and webhook
		// (signed X-Arb-Signature, SSRF-hardened, retried with backoff)
		// alert channels. Both sinks always exist; email simply reports
		// "not configured" per delivery until an operator sets smtp_url.
		evaluator.SetChannels(
			&notification.EmailSink{Transport: &notification.SMTPTransport{URL: func(ctx context.Context) string {
				v, _, _ := secretsMgr.Get(ctx, "smtp_url")
				return v
			}}},
			notification.NewWebhookSink(),
		)
		executor := paperexec.New(screenerSvc, ledger, log, paperexec.Options{Seed: 1, IDGen: newULID, Entitle: tenant.paperEntitle(ledger)})
		evaluator.OnOpen(executor.OnOpen)
		screenerSvc.SetAutoPaper(executor)
		// An edited paper.balances must reach the executor's wallets;
		// without this it latches them at first load (T-096).
		screenerSvc.OnSettingsApplied = func(screener.Snapshot) { executor.ReloadWallets() }
		others = append(others, screener.NewAutomation(screenerSvc, executor, evaluator))
		if mtr != nil {
			if err := mtr.RegisterScreener(screenerMetricSources(screenerSvc, evaluator, executor, &feedRateLimited)); err != nil {
				log.Error("screener metrics registration failed", "error", err)
			}
		}

		// T-078: nightly (00:05 UTC) and on-demand paper reports per
		// strategy / rule over the same ledger; files under
		// <ARB_RECORDING_DIR>/screener-reports/<date>/, rows in
		// screener_reports (migration 000012), one Telegram summary.
		var reportStore report.Store = report.NewMemoryStore()
		if store != nil {
			reportStore = store.ScreenerReports()
		}
		screenerReports = &report.Generator{Svc: screenerSvc, Ledger: ledger, Store: reportStore, Notify: notify.Notify,
			Dir: cfg.RecordingDir, Log: log, IDGen: newULID, Seed: 1}
		// The nightly run covers every organisation separately (one
		// ledger, one rule set, one document each); without a database
		// only the platform organisation exists.
		if tenant.store != nil {
			screenerReports.Orgs = tenant.store
		}
		others = append(others, &report.Scheduler{Gen: screenerReports})
	}

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

	// AI advisor (T-059 D4): the Service always exists, behind an
	// ai.Switch holding a possibly-nil provider, so an enable at runtime
	// takes effect without a redeploy. The platform-settings subscriber
	// rebuilds the provider through buildAdvisor (resolving the key via
	// the secrets chain) on every swap; a key written to the vault
	// applies immediately.
	aiSwitch := &ai.Switch{}
	aiSvc := &ai.Service{
		Advisor: aiSwitch, Strategy: stratSvc,
		Notify: notify.Notify, Log: log, IDGen: newULID,
		Limits: func() ai.Budget {
			b := platformSvc.Current().Settings.AI.Budget
			return ai.Budget{MaxAnalysesPerDay: b.MaxAnalysesPerDay, MaxOutputTokens: b.MaxOutputTokens}
		},
	}
	if store != nil {
		aiSvc.Store = store.AI()
	}
	if mtr != nil {
		if err := mtr.RegisterAI(aiSvc.Requests, aiSvc.Failures); err != nil {
			log.Error("ai metrics registration failed", "error", err)
		}
	}
	aiState := &aiRuntime{}
	applier := &aiApplier{log: log, sw: aiSwitch, state: aiState, src: secretsMgr}
	// Boot: resolve synchronously (Current() takes no lock) so the status
	// route and the scheduler see a built advisor before Run starts.
	applier.apply(platformSvc.Current().Settings.AI, applier.next(platformSvc.Current().Settings.AI))
	// Swaps: the subscriber runs INSIDE the platform writer lock, so it
	// must not do the vault round trip itself. It compares the ai section
	// with the last one applied and, only when it changed, hands the
	// rebuild to a goroutine; a generation counter drops stale results
	// (P3-1/P3-3).
	platformSvc.Subscribe(func(snap platform.Snapshot) { applier.request(snap.Settings.AI, false) })
	// A key written to (or removed from) the vault re-resolves the
	// advisor at once — applies:"immediately" is literally true. The
	// Telegram token is read once at boot (D5) and is not re-applied.
	secretsMgr.OnChange = func(name string) {
		if name == "anthropic_api_key" {
			applier.request(platformSvc.Current().Settings.AI, true)
		}
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
		feedRateLimited = engine.FeedRateLimited
		engine.Notifier = notify
		engine.Center = center
		// The engine is no longer an app.Component in its own right: the
		// supervisor built below re-enters Engine.Run across restarts
		// (T-057 D4 — one stable *Engine value, never replaced). Every
		// existing seam that closes over `engine` (recorderProxy,
		// paperProxy, ScannerStatus, Reads, engine.Hub, AIInput,
		// telegramServices) keeps working unchanged.
		platformSvc.Catalog = engineCatalog{engine} // preferred over the storage fallback set above

		// Always appended (D4): the cadence follows ai.schedule at every
		// wake, and a disabled advisor makes each fire a cheap skip.
		others = append(others, &ai.Scheduler{
			Service:  aiSvc,
			InputFor: engine.AIInput,
			Log:      log,
			Cadence: func() ai.Cadence {
				c := platformSvc.Current().Settings.AI.Schedule
				return ai.Cadence{HourlyMinutes: c.HourlyMinutes, DailyHours: c.DailyHours, WeeklyHours: c.WeeklyHours}
			},
		})

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
			if mtr != nil {
				if err := mtr.RegisterCampaign(campaigns.RunCounts); err != nil {
					log.Error("campaign metrics registration failed", "error", err)
				}
			}

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
		allow.SetDisabled(platformSvc.Current().Settings.Telegram.Disabled)
		platformSvc.Subscribe(func(snap platform.Snapshot) {
			// telegram.disabled is hot too (T-059 §4.2): commands and
			// pushes go quiet together through the one shared set.
			allow.Set(snap.Settings.Telegram.Allowlist)
			allow.SetDisabled(snap.Settings.Telegram.Disabled)
		})
		telegramAllow = allow

		// Telegram control surface: only with a token, a non-empty
		// allow-list, and an engine to control. The allowlist that
		// gates construction is the SETTINGS document's (v1 == Seed(cfg)
		// on a fresh install, so behavior matches today exactly); an
		// empty boot allowlist never builds the bot at all — the
		// honest caveat field_timing surfaces (design §1.3). The token
		// resolves through the secrets chain ONCE, here: a token written
		// to the vault later applies on the next process restart (D5),
		// which the secrets API reports as applies:"process_restart".
		tokenCtx, tokenCancel := context.WithTimeout(context.Background(), 5*time.Second)
		telegramToken, tokenSource, _ := secretsMgr.Get(tokenCtx, "telegram_bot_token")
		tokenCancel()
		switch {
		case telegramToken == "":
			telegramReason.Store("no token configured")
		case len(allow.Members()) == 0:
			telegramReason.Store("allowlist empty at boot")
		}
		if telegramToken != "" && len(allow.Members()) > 0 {
			client := telegram.NewClient("https://api.telegram.org/bot" + telegramToken)
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
			log.Info("telegram bot enabled", "allowlisted_users", len(allow.Members()), "token_source", tokenSource)
		} else if telegramToken != "" {
			log.Warn("telegram token configured but the platform settings allowlist is empty; bot disabled (allow-list is mandatory)")
		}
		telegramToken = "" //nolint:ineffassign // drop the reference as soon as the client holds it

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
		// audit S3/P1-12: disabling an account must cascade-revoke its
		// API keys; AdminService does this itself (not the HTTP layer)
		// so Telegram — a future caller of the same service — gets the
		// same guarantee without having to remember it. tenant.apiKeys
		// is nil in database-less profiles, same as apiServer.APIKeys
		// below; AdminService already treats a nil APIKeys as "no keys
		// in this profile", not a bug.
		userAdmin.APIKeys = tenant.apiKeys
		cascadeAudit := webAudit(log, store)
		userAdmin.AuditCascade = func(actor, action, entity string) {
			cascadeAudit(actor, action, entity, "", "", nil)
		}
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
		apiServer.Telegram = telegramStatusView(bot, push, telegramAllow, botRunning.Load, func() string {
			r, _ := telegramReason.Load().(string)
			return r
		})
		apiServer.Secrets = secretsMgr
		apiServer.Tenancy = tenant.store
		apiServer.Entitlements = tenant.resolver
		apiServer.Billing = tenant.billing
		apiServer.RiskAckVersion = RiskDisclosureVersion
		apiServer.APIKeys = tenant.apiKeys
		apiServer.APIRateLimiter = tenant.apiRateLimiter
		apiServer.Screener = screenerSvc
		apiServer.ScreenerReports = screenerReports
		apiServer.AIStatus = func() api.AIRuntimeStatus {
			st := aiState.get()
			u := aiSvc.Usage()
			return api.AIRuntimeStatus{
				Enabled: st.Enabled, Running: st.Running, Reason: st.Reason,
				Provider: st.Provider, Model: st.Model, KeySource: st.KeySource,
				AnalysesToday: u.AnalysesToday, MaxPerDay: u.MaxPerDay, LastAnalysis: u.LastAnalysis,
			}
		}
		// platform.allowed_origin is hot (D7): the websocket origin check
		// reads the atomic the subscriber updates.
		platformSvc.Subscribe(func(snap platform.Snapshot) {
			apiServer.SetAllowedOrigin(snap.Settings.Platform.AllowedOrigin)
		})
		// The API profile has no engine, so it reports the CONFIGURED
		// mode; engine profiles report the RUNNING one (below).
		apiServer.Mode = func() string { return string(platformSvc.Current().Settings.Platform.Mode) }
		if engine != nil {
			apiServer.ScannerStatus = func() any { return engine.Status() }
			apiServer.Reads = NewReadModel(engine, stratSvc)
			apiServer.Triangles = NewTriangleReader(engine)
			engine.Hub = hub
			apiServer.Mode = func() string { return string(engine.Mode()) }
			// Wired unconditionally (T-059 §2.3): paperProxy is nil-safe
			// (Running() false, Pause/Resume no-ops) and gating on the
			// boot mode would 404 the paper routes after a switch into
			// PAPER until a redeploy.
			apiServer.Paper = paperProxy{engine}
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
				data := map[string]any{
					// T-059 §2.3: the console banner shows the RUNNING mode
					// and annotates a differing configured one.
					"mode": map[string]any{
						"running":    string(engine.Mode()),
						"configured": string(platformSvc.Current().Settings.Platform.Mode),
					},
					"restart": supervisor.Status(),
					"engine":  engine.Status(),
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

// aiRuntime is the advisor's current runtime state for the status
// route: what the document asks for versus what is installed.
type aiRuntime struct {
	p atomic.Pointer[aiRuntimeState]
}

type aiRuntimeState struct {
	Enabled, Running                   bool
	Reason, Provider, Model, KeySource string
}

func (a *aiRuntime) set(st aiRuntimeState) { a.p.Store(&st) }

// aiApplier serialises advisor rebuilds: one mutex guards the last
// requested ai section, the generation counter, and the paired
// aiSwitch/aiState stores, so the status view never describes a
// different advisor than the one running. buildAdvisor (the secrets
// round trip) runs outside every lock.
type aiApplier struct {
	log   *slog.Logger
	sw    *ai.Switch
	state *aiRuntime
	src   secrets.SecretSource

	mu      sync.Mutex
	last    platform.AISettings
	hasLast bool
	gen     uint64 // last generation requested
	applied uint64 // last generation stored
}

// next records settings as the latest request and returns its
// generation. Callers then build outside the lock and hand the result
// to apply, which drops it when a newer generation has already landed.
func (a *aiApplier) next(settings platform.AISettings) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last, a.hasLast = settings, true
	a.gen++
	return a.gen
}

// request schedules a rebuild unless the ai section is unchanged since
// the last one (force bypasses the comparison: the key changed, not the
// document). AISettings has no reference fields, so == is a deep compare.
func (a *aiApplier) request(settings platform.AISettings, force bool) {
	a.mu.Lock()
	if !force && a.hasLast && a.last == settings {
		a.mu.Unlock()
		return
	}
	a.last, a.hasLast = settings, true
	a.gen++
	gen := a.gen
	a.mu.Unlock()
	go a.apply(settings, gen)
}

func (a *aiApplier) apply(settings platform.AISettings, gen uint64) {
	adv, st := buildAdvisor(settings, a.log, a.src)
	a.mu.Lock()
	defer a.mu.Unlock()
	if gen < a.applied {
		return // a newer request already landed; keep it
	}
	a.applied = gen
	a.sw.Set(adv)
	a.state.set(st)
	if adv != nil {
		a.log.Info("ai advisor enabled", "provider", adv.Name(), "model", adv.Model(), "key_source", st.KeySource)
	} else {
		a.log.Info("ai advisor idle", "reason", st.Reason)
	}
}

func (a *aiRuntime) get() aiRuntimeState {
	if p := a.p.Load(); p != nil {
		return *p
	}
	return aiRuntimeState{Reason: "not configured yet"}
}

// buildAdvisor selects the AI provider from the ai.* settings section
// (T-059 §4.1), resolving the key through the secrets chain (vault
// first, env fallback — T-060). The key never leaves the process except
// in the provider's auth header and is never logged. A nil advisor with
// a reason is the honest "enabled but idle" state the status route and
// the apply-time warning report.
func buildAdvisor(settings platform.AISettings, log *slog.Logger, src secrets.SecretSource) (ai.Advisor, aiRuntimeState) {
	st := aiRuntimeState{Enabled: settings.Enabled, Provider: settings.Provider, Model: settings.Model}
	if !settings.Enabled {
		st.Reason = "disabled in settings"
		return nil, st
	}
	switch settings.Provider {
	case "fake":
		st.Running = true
		return ai.Fake{}, st
	case "anthropic":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		key, source, ok := src.Get(ctx, "anthropic_api_key")
		cancel()
		if !ok {
			st.Reason = "no anthropic_api_key"
			return nil, st
		}
		adv := ai.NewAnthropic(key, settings.Model)
		adv.MaxTokens = settings.Budget.MaxOutputTokens
		st.Running, st.KeySource = true, source
		return adv, st
	default:
		// Unreachable through Validate; kept so a stored document from a
		// future provider list never panics an older binary.
		log.Warn("unknown ai.provider; advisor idle", "provider", settings.Provider)
		st.Reason = "provider " + settings.Provider + " not built"
		return nil, st
	}
}

// buildSecrets opens the vault (T-060 §3.2 boot policy): ARB_SECRET_KEY
// is read inside internal/secrets, never through config.Bootstrap. An
// unset key logs at WARN and leaves env-provided secrets working; an
// invalid key logs at ERROR with the same outcome (the vault refuses to
// start rather than run under a key nobody can rotate). Without
// persistence there is nowhere durable to write, so the vault stays
// closed with that reason rather than silently losing writes on the
// next process restart.
func buildSecrets(log *slog.Logger, store *storage.Store, cfg config.Bootstrap) *secrets.Manager {
	env := secrets.Env{
		"anthropic_api_key":     cfg.AnthropicAPIKey,
		"telegram_bot_token":    cfg.TelegramToken,
		"paddle_api_key":        cfg.PaddleAPIKey,
		"paddle_webhook_secret": cfg.PaddleWebhookSecret,
	}
	key, err := secrets.KeyFromEnv()
	switch {
	case errors.Is(err, secrets.ErrNoKey):
		log.Warn("secrets vault disabled: ARB_SECRET_KEY unset; env-provided secrets still work")
		return secrets.NewManager(nil, "ARB_SECRET_KEY unset", env)
	case err != nil:
		log.Error("secrets vault disabled: ARB_SECRET_KEY invalid; env-provided secrets still work", "error", err)
		return secrets.NewManager(nil, err.Error(), env)
	}
	if store == nil {
		log.Warn("secrets vault disabled: ARB_DATABASE_URL unset (nowhere durable to write); env-provided secrets still work")
		return secrets.NewManager(nil, "ARB_DATABASE_URL unset: the vault needs persistence", env)
	}
	vault, err := secrets.NewVault(store.Secrets(), key)
	if err != nil {
		log.Error("secrets vault disabled", "error", err)
		return secrets.NewManager(nil, err.Error(), env)
	}
	vault.Log = log
	log.Info("secrets vault open", "key_id", vault.KeyID())
	return secrets.NewManager(vault, "", env)
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
func telegramStatusView(bot *telegram.Bot, push *telegram.PushSink, allow *allowSet, enabled func() bool, reason func() string) func() api.TelegramStatusView {
	return func() api.TelegramStatusView {
		view := api.TelegramStatusView{Enabled: enabled()}
		if allow != nil {
			ids := allow.Members()
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			view.Allowlist = ids
			view.Disabled = allow.Disabled()
		}
		switch {
		case view.Disabled:
			view.Reason = "disabled in settings"
		case !view.Enabled && reason != nil:
			view.Reason = reason()
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
//
// disabled (T-059 §4.2, telegram.disabled) mutes both reads together:
// Allowed() returns false and IDs() returns nil, so commands and pushes
// go quiet as one — the exact invariant the shared set protects.
type allowSet struct {
	p        atomic.Pointer[map[int64]bool]
	disabled atomic.Bool
}

func (a *allowSet) Set(ids []int64) {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	a.p.Store(&m)
}

// SetDisabled applies telegram.disabled (hot).
func (a *allowSet) SetDisabled(v bool) { a.disabled.Store(v) }

// Disabled reports the mute state.
func (a *allowSet) Disabled() bool { return a.disabled.Load() }

func (a *allowSet) Allowed(id int64) bool {
	if a.disabled.Load() {
		return false
	}
	if m := a.p.Load(); m != nil {
		return (*m)[id]
	}
	return false
}

func (a *allowSet) IDs() []int64 {
	if a.disabled.Load() {
		return nil
	}
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

// Members returns the configured allowlist regardless of the mute
// state (for the status view; never used for delivery decisions).
func (a *allowSet) Members() []int64 {
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
func webAudit(log *slog.Logger, store *storage.Store) func(actor, action, entity, ip, correlationID string, after []byte) {
	return func(actor, action, entity, ip, correlationID string, after []byte) {
		if store == nil {
			log.Info("audit event (memory-only)", "actor", actor, "action", action,
				"entity", entity, "source", "web", "ip", ip, "correlation_id", correlationID)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.InsertAuditEvent(ctx, storage.AuditRow{
			ID: newULID(), Actor: actor, Source: "web",
			Action: action, Entity: entity, After: after, IP: ip, CorrelationID: correlationID,
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := svc.Load(ctx, cfg); err != nil {
		return nil, fmt.Errorf("app: platform settings load failed: %w", err)
	}
	return svc, nil
}

// buildScreener wires the Scanner Suite backend core (T-067/T-068):
// DB-backed settings/rules/events/templates/funding rows when
// persistence is enabled, in-memory otherwise (mirrors buildPlatform
// exactly, down to the P2-4 hard-failure-on-load-error policy — a
// settings document that failed to load must not silently become a
// re-seeded MemoryStore, discarding whatever was persisted). Unlike
// platform.Seed, screener.Defaults() takes no bootstrap env — the
// document's v1 seed is fixed, so this takes no cfg.
func buildScreener(log *slog.Logger, store *storage.Store) (*screener.Service, error) {
	var (
		st        screener.SettingsStore
		rules     screener.RuleStore
		events    screener.EventStore
		templates screener.TemplateStore
		funding   screener.FundingStore
		audit     func(context.Context, screener.AuditEvent)
	)
	if store != nil {
		st = store.ScreenerSettings()
		rules = store.ScreenerRules()
		events = store.ScreenerEvents()
		templates = store.ScreenerTemplates()
		funding = store.ScreenerFunding()
		audit = func(ctx context.Context, ev screener.AuditEvent) {
			row := storage.AuditRow{
				ID: newULID(), Actor: ev.Actor, Source: ev.Source,
				Action: ev.Action, Entity: ev.Entity, EntityID: ev.EntityID,
				Before: ev.Before, After: ev.After,
			}
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := store.InsertAuditEvent(ctx, row); err != nil {
				log.Error("screener settings audit insert failed", "action", ev.Action, "error", err)
			}
		}
	} else {
		st = screener.NewMemoryStore()
		rules = screener.NewMemoryRuleStore()
		events = screener.NewMemoryEventStore()
		templates = screener.NewMemoryTemplateStore()
		funding = screener.NewMemoryFundingStore()
	}
	svc := screener.NewService(screener.NewBook(), st, log, audit)
	svc.Rules, svc.Events, svc.Templates, svc.Funding = rules, events, templates, funding
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := svc.Load(ctx); err != nil {
		return nil, fmt.Errorf("app: screener settings load failed: %w", err)
	}
	return svc, nil
}

// buildAuth wires auth stores: pgx-backed when persistence is enabled
// (sessions survive restarts), in-memory otherwise. The bootstrap admin
// from the environment is created ONLY when no account holds that
// email yet (audit S6/P1-14; see bootstrapAdmin) — never re-upserted on
// every boot. It also returns the users & roles admin service (BL-11)
// over the same backing store, so console user management works
// identically with or without a database.
func buildAuth(cfg config.Bootstrap, log *slog.Logger, store *storage.Store) (*auth.Manager, *auth.AdminService) {
	var users auth.UserStore
	var sessions auth.SessionStore
	var admin auth.AdminStore

	if store != nil {
		as := store.Auth()
		users, sessions, admin = as, as, as
	} else {
		mem := auth.NewMemoryStore()
		users, sessions, admin = mem, mem, mem
	}
	bootstrapAdmin(cfg, log, admin)

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

// bootstrapAdmin creates the operator's first administrator account
// when ARB_ADMIN_EMAIL/ARB_ADMIN_PASSWORD are configured. It is
// insert-only (audit S6/P1-14): admin.CreateUser refuses with
// ErrDuplicateEmail rather than overwriting a row that already holds
// this email, and that is treated as success here, not failure — the
// console (or a previous boot, before this fix) may since have changed
// that account's password, role or disabled status, and re-asserting
// the environment's values on every restart would silently undo
// exactly that, turning the bootstrap credential into a standing
// backdoor. config.Load already refuses to start with a bootstrap
// password shorter than the platform minimum or equal to the
// documented example value, so a value reaching here is at least not
// trivially guessable.
func bootstrapAdmin(cfg config.Bootstrap, log *slog.Logger, admin auth.AdminStore) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch err := admin.CreateUser(ctx, u); {
	case err == nil:
		log.Info("bootstrap admin created", "email", cfg.AdminEmail)
	case errors.Is(err, auth.ErrDuplicateEmail):
		log.Info("bootstrap admin email already registered; leaving the existing account untouched", "email", cfg.AdminEmail)
	default:
		log.Error("bootstrap admin store failed", "error", err)
	}
}
