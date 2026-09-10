// Package api hosts the versioned HTTP API (/api/v1) and, later, the
// realtime WebSocket hub. Handlers stay thin: business logic lives in
// application services, never here.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/ai"
	"github.com/cploutarchou/arb-chain-bot/internal/apikey"
	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/billing/paddle"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
	"github.com/cploutarchou/arb-chain-bot/internal/realtime"
	"github.com/cploutarchou/arb-chain-bot/internal/reporting"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/report"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// BuildInfo describes what this process build actually contains; the
// status endpoint reports it verbatim so operators always see the truth.
type BuildInfo struct {
	Version    string   `json:"version"`
	Commit     string   `json:"commit"`
	Components []string `json:"components"`
}

// Server is the HTTP API component.
type Server struct {
	cfg     config.Bootstrap
	log     *slog.Logger
	info    BuildInfo
	start   time.Time
	csrfKey []byte

	// Auth gates every route beyond health probes and login; nil means
	// auth is unconfigured and gated routes answer 503.
	Auth *auth.Manager
	// Hub, when set, serves /api/v1/ws.
	Hub *realtime.Hub
	// ScannerStatus, when set, backs /api/v1/scanner/status.
	ScannerStatus func() any
	// Paper, when set, backs the paper control routes (PAPER mode only).
	Paper PaperController
	// Breakers, when set, backs the operator's breaker acknowledgement
	// route (POST /api/v1/risk/breakers/close) — engine profiles only;
	// without an engine there is no registry to close.
	Breakers BreakerController
	// Strategy, when set, backs the versioned config routes.
	Strategy *strategy.Service
	// MetricsHandler, when set, serves GET /metrics on this mux (dev
	// convenience; production sets ARB_METRICS_ADDR for a private port).
	MetricsHandler http.Handler
	// ObserveRequest, when set, records api_request_duration per request.
	ObserveRequest func(method, route string, status int, seconds float64)
	// Alerts, when set, backs the alert-center routes.
	Alerts *notification.Center
	// AI, when set, backs the advisor routes.
	AI *ai.Service
	// AuditAction records control actions (source=web) with the caller's
	// IP and correlation ID for forensics (audit S-008); nil = log only.
	// after is an optional JSON payload for the row's "after" column
	// (nil for actions with no safe state to record).
	AuditAction func(actor, action, entity, ip, correlationID string, after []byte)
	// Reads, when set, backs the live read groups (engine profiles).
	Reads ReadModel
	// Store, when set, backs the history read groups.
	Store *storage.Store
	// Reports, when set, backs on-demand report generation.
	Reports *reporting.Generator
	// Recorder, when set, backs the in-process recording controls.
	Recorder RecorderController
	// Campaigns, when set, backs the §80 campaign routes.
	Campaigns CampaignService
	// Users, when set, backs the users & roles console routes (BL-11).
	Users UserAdmin
	// Platform, when set, backs the versioned platform-settings routes
	// (T-057: venues/symbols/fees/paper balances/Telegram allowlist).
	Platform *platform.Service
	// PlatformCatalog, when set, backs the "plan" (markets/triangles)
	// field on GET/preview/apply. nil omits "plan" from the response
	// rather than failing the request.
	PlatformCatalog platform.Catalog
	// Restart, when set, backs the supervised-engine restart routes.
	Restart RestartController
	// BotRunning, when set, reports whether the Telegram bot was
	// actually constructed — field_timing's "telegram.allowlist" is hot
	// only then (components.go:170: an empty boot allowlist never
	// builds the bot, so the first entry needs a restart).
	BotRunning func() bool
	// Telegram, when set, backs GET /api/v1/telegram/status (BL-21).
	// nil means the token was never configured; the route still answers
	// 200 with enabled:false rather than 404 — "not configured" is a
	// legitimate status, not an absent route.
	Telegram func() TelegramStatusView
	// Triangles, when set, backs the live half of GET
	// /api/v1/triangles/{id} (BL-26); nil (no engine in this profile)
	// leaves the route serving only store-backed history.
	Triangles TriangleReader
	// Replays, when set, backs the console-driven replay routes (BL-17).
	Replays ReplayService
	// Mode, when set, reports the operating mode for /system/status
	// (T-059: the engine's RUNNING mode in engine profiles, the
	// configured document's mode in the API profile). nil falls back to
	// the boot cfg.Mode.
	Mode func() string
	// Secrets, when set, backs the write-only secrets routes (T-060).
	Secrets SecretsAdmin
	// AIStatus, when set, backs GET /api/v1/ai/status and the
	// "warnings" field on a platform-settings apply.
	AIStatus func() AIRuntimeStatus
	// Screener, when set, backs the Scanner Suite routes (T-067/T-068:
	// spreads/perpetuals/funding/calculator/settings/rules/events/
	// templates/auto-paper). nil (no engine profile runs it yet) 503s
	// those routes rather than 404ing — the subsystem exists in this
	// build, it just is not wired in this profile.
	Screener *screener.Service
	// ScreenerReports is the T-078 nightly report generator (nil when no
	// executor/ledger runs in this profile → /screener/reports 503s).
	ScreenerReports *report.Generator
	// Tenancy, when set, resolves the caller's organisation and
	// membership on every authenticated request (T-081). nil = every
	// account acts in the platform organisation (database-less
	// profiles; see resolvePrincipal).
	Tenancy tenancy.Store
	// Entitlements, when set, resolves the organisation's effective
	// entitlements (packages.md §3) and caches them; nil falls back to
	// the bare package document.
	Entitlements *entitlements.Resolver
	// Billing, when set, backs the Paddle routes (T-083). nil 503s them.
	Billing *paddle.Service
	// RiskAckVersion is the risk-disclosure version every organisation
	// must acknowledge before using the product (compliance review #3);
	// "" disables the gate.
	RiskAckVersion string
	// APIKeys, when set, backs the client API-key routes and the Bearer
	// authentication path in authenticate() (T-086). nil: /org/api-keys
	// answers 503 and Authorization: Bearer headers are ignored (no
	// database, no tenancy — matches Tenancy/Entitlements' own nil
	// meaning "not available in this profile").
	APIKeys apikey.Store
	// APIRateLimiter enforces api.rate_per_min/api.burst per API key
	// (packages.md §3.2); nil disables rate limiting for Bearer callers
	// (database-less profiles only, where APIKeys is also nil).
	APIRateLimiter *entitlements.RateLimiter

	// allowedOrigin is platform.allowed_origin (hot, D7): the websocket
	// origin check reads it through an atomic accessor because the
	// Supervisor never rebuilds the api.Server.
	allowedOrigin atomic.Pointer[string]
	// apiKeyTouch throttles the last_used_at write to at most once per
	// apiKeyTouchInterval per key id, so a hot API key does not turn
	// every request into a synchronous UPDATE.
	apiKeyTouch sync.Map
}

// apiKeyTouchInterval bounds how often authenticateAPIKey persists
// last_used_at for a given key (write-amplification guard, T-086).
const apiKeyTouchInterval = time.Minute

// AIRuntimeStatus is the advisor's runtime status line (settings-
// expansion §4.1): the configured intent (enabled) versus what is
// actually installed (running), with the reason when they differ.
type AIRuntimeStatus struct {
	Enabled       bool       `json:"enabled"`
	Running       bool       `json:"running"`
	Reason        string     `json:"reason,omitempty"`
	Provider      string     `json:"provider"`
	Model         string     `json:"model"`
	KeySource     string     `json:"key_source,omitempty"` // vault | env
	AnalysesToday int        `json:"analyses_today"`
	MaxPerDay     int        `json:"max_per_day"`
	LastAnalysis  *time.Time `json:"last_analysis,omitempty"`
}

// PaperController is the paper engine's control surface (shared with
// Telegram; single backend state).
type PaperController interface {
	Pause()
	Resume()
	Running() bool
	// Reset rebuilds the paper portfolio/reservations to the configured
	// initial balances and starts a new paper session row, preserving
	// historical cycles/orders (BL-10). Returns ErrPaperNotIdle if the
	// engine is running or a simulation is in flight — callers must
	// Pause() first.
	Reset(ctx context.Context) error
}

// ErrPaperNotIdle is returned by PaperController.Reset when the engine
// is still running or a simulation is in flight (BL-10); the HTTP layer
// maps it to 409.
var ErrPaperNotIdle = errors.New("api: paper engine must be paused with zero active simulations before reset")

func NewServer(cfg config.Bootstrap, log *slog.Logger, info BuildInfo) *Server {
	if info.Version == "" {
		info.Version = "dev"
	}
	s := &Server{cfg: cfg, log: log, info: info, start: time.Now(), csrfKey: newCSRFKey()}
	origin := cfg.AllowedOrigin
	s.allowedOrigin.Store(&origin)
	return s
}

func (s *Server) Name() string { return "api" }

// SetAllowedOrigin applies platform.allowed_origin at runtime (hot).
func (s *Server) SetAllowedOrigin(origin string) {
	s.allowedOrigin.Store(&origin)
}

// AllowedOrigin returns the origin currently admitted cross-origin.
func (s *Server) AllowedOrigin() string {
	if p := s.allowedOrigin.Load(); p != nil {
		return *p
	}
	return s.cfg.AllowedOrigin
}

// mode resolves the reported operating mode (see Mode).
func (s *Server) mode() string {
	if s.Mode != nil {
		return s.Mode()
	}
	return string(s.cfg.Mode)
}

// HTTP server envelope (audit S13). Every phase a client can stretch is
// bounded: headers 5 s, body read 30 s, response write 60 s, keep-alive
// idle 120 s, headers 64 KiB. The WebSocket route upgrades via
// connection hijack, so these timeouts govern only its handshake and
// never cut an established socket; request bodies are additionally
// bounded per handler.
const (
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 60 * time.Second
	httpIdleTimeout       = 120 * time.Second
	httpMaxHeaderBytes    = 1 << 16
)

// newHTTPServer builds the API listener with the S13 envelope applied.
func (s *Server) newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           s.withRequestLog(handler),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
	}
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	s.routes(mux)

	srv := s.newHTTPServer(mux)

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("api listening", "addr", s.cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownGrace)
	defer cancel()
	return srv.Shutdown(shutCtx)
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		// Ready = serving AND (when persistence is on) the pool answers.
		// Feeds degrade rather than gate readiness: the API must stay up
		// to show WHY books are unhealthy.
		if !s.storeHealthy(r.Context()) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("database unreachable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	// CSRF on logout too (audit S-013): a cross-site logout is a nuisance
	// attack, and the wrap costs nothing.
	mux.HandleFunc("POST /api/v1/auth/logout", s.requireAuth(s.requireCSRF(s.handleLogout)))
	// /me is reachable before the risk acknowledgement so the console
	// can learn that it must show the disclosure (compliance #3).
	mux.HandleFunc("GET /api/v1/auth/me", s.requireAuthPreAck(s.handleMe))
	mux.HandleFunc("GET /api/v1/me", s.requireAuthPreAck(s.handleMe))

	mux.HandleFunc("GET /api/v1/system/status", s.requirePerm(auth.PermViewSystem, func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, http.StatusOK, map[string]any{
			"mode":       s.mode(),
			"version":    s.info.Version,
			"commit":     s.info.Commit,
			"uptime_sec": int64(time.Since(s.start).Seconds()),
			"components": s.info.Components,
		})
	}))
	mux.HandleFunc("GET /api/v1/scanner/status", s.requirePerm(auth.PermViewDashboard, func(w http.ResponseWriter, r *http.Request) {
		if s.ScannerStatus == nil {
			WriteError(w, http.StatusNotFound, "scanner_absent", "scanner not running in this profile", correlationID(r))
			return
		}
		WriteData(w, http.StatusOK, s.ScannerStatus())
	}))
	// Paper controls (RBAC + CSRF). Outside PAPER mode the engine is absent
	// and the routes answer 404 honestly.
	paperGate := func(action string, fn func(PaperController)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Paper == nil {
				WriteError(w, http.StatusNotFound, "paper_absent", "paper engine not running (mode is not PAPER)", correlationID(r))
				return
			}
			p, _ := PrincipalFrom(r.Context())
			fn(s.Paper)
			s.audit(r, p.UserID, action, "paper_engine")
			s.log.Info("paper engine control",
				"actor", p.UserID, "path", r.URL.Path, "running", s.Paper.Running())
			WriteData(w, http.StatusOK, map[string]any{"running": s.Paper.Running()})
		}
	}
	mux.HandleFunc("POST /api/v1/paper/pause", s.requirePerm(auth.PermPaperControl, s.requireCSRF(paperGate("paper.pause", func(p PaperController) { p.Pause() }))))
	mux.HandleFunc("POST /api/v1/paper/resume", s.requirePerm(auth.PermPaperControl, s.requireCSRF(paperGate("paper.resume", func(p PaperController) { p.Resume() }))))
	s.paperResetRoute(mux)
	mux.HandleFunc("GET /api/v1/ws", s.requireAuth(s.handleWS))
	s.configRoutes(mux)
	s.alertRoutes(mux)
	s.aiRoutes(mux)
	s.riskRoutes(mux)
	s.readRoutes(mux)
	s.pnlRoutes(mux)
	s.triangleRoutes(mux)
	s.reportRoutes(mux)
	s.opsRoutes(mux)
	s.replayRoutes(mux)
	s.usersRoutes(mux)
	s.platformRoutes(mux)
	s.secretsRoutes(mux)
	s.telegramRoutes(mux)
	s.screenerRoutes(mux)
	s.orgRoutes(mux)
	s.apiKeyRoutes(mux)
	s.billingRoutes(mux)
	if s.MetricsHandler != nil {
		// Same-mux dev convenience stays operator-only (audit S5:
		// metric names and label values map the platform's internals,
		// including per-tenant scanner activity — not a VIEWER surface).
		// Production sets ARB_METRICS_ADDR for a private port instead.
		mux.HandleFunc("GET /metrics", s.requirePlatformAdmin(func(w http.ResponseWriter, r *http.Request) {
			s.MetricsHandler.ServeHTTP(w, r)
		}))
	}
}

// audit records one web control action with request forensics; nil-safe.
func (s *Server) audit(r *http.Request, actor, action, entity string) {
	s.auditWith(r, actor, action, entity, nil)
}

// auditWith is audit with a structured "after" payload (marshalled here;
// a value that does not marshal is recorded without a payload rather
// than dropping the event). Callers pass only state that is safe to
// persist — never a secret value.
func (s *Server) auditWith(r *http.Request, actor, action, entity string, after any) {
	if s.AuditAction == nil {
		return
	}
	var payload []byte
	if after != nil {
		if b, err := json.Marshal(after); err == nil {
			payload = b
		} else {
			s.log.Error("audit payload not marshalled", "action", action, "error", err)
		}
	}
	s.AuditAction(actor, action, entity, s.clientAddr(r).String(), correlationID(r), payload)
}

func (s *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// Health probes and scrapes are noise in both logs and metrics.
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
			return
		}
		if s.ObserveRequest != nil {
			// The mux stamps the matched pattern on the request; bounded
			// route label, never the raw path.
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			s.ObserveRequest(r.Method, route, rec.status, time.Since(start).Seconds())
		}
		s.log.Debug("http request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"correlation_id", correlationID(r),
		)
	})
}

// statusRecorder captures the response code; Hijack passes through so
// the websocket upgrade keeps working behind the wrapper.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("api: underlying writer does not support hijacking")
	}
	return h.Hijack()
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Envelope is the uniform API response shape (docs/architecture.md §7).
type Envelope struct {
	Data  any       `json:"data"`
	Error *APIError `json:"error"`
}

type APIError struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

func WriteData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, Envelope{Data: data})
}

func WriteError(w http.ResponseWriter, status int, code, msg, correlationID string) {
	writeJSON(w, status, Envelope{Error: &APIError{Code: code, Message: msg, CorrelationID: correlationID}})
}

// WriteErrorData writes an error alongside a data payload (e.g.
// stale_version's current_version) — the envelope keeps both fields so
// the client never has to parse the message string for machine data.
func WriteErrorData(w http.ResponseWriter, status int, code, msg, correlationID string, data any) {
	writeJSON(w, status, Envelope{Data: data, Error: &APIError{Code: code, Message: msg, CorrelationID: correlationID}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Belt-and-braces against MIME sniffing on API responses that may
	// echo user-influenced strings (audit S-011).
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
