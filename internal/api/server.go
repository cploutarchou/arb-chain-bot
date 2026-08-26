// Package api hosts the versioned HTTP API (/api/v1) and, later, the
// realtime WebSocket hub. Handlers stay thin: business logic lives in
// application services, never here.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/realtime"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
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
	// Strategy, when set, backs the versioned config routes.
	Strategy *strategy.Service
}

// PaperController is the paper engine's control surface (shared with
// Telegram; single backend state).
type PaperController interface {
	Pause()
	Resume()
	Running() bool
}

func NewServer(cfg config.Bootstrap, log *slog.Logger, info BuildInfo) *Server {
	if info.Version == "" {
		info.Version = "dev"
	}
	return &Server{cfg: cfg, log: log, info: info, start: time.Now(), csrfKey: newCSRFKey()}
}

func (s *Server) Name() string { return "api" }

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	s.routes(mux)

	srv := &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           s.withRequestLog(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

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
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		// Readiness gains real dependency checks (DB, feeds) as those
		// components land; for now the process is ready once serving.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET /api/v1/auth/me", s.requireAuth(s.handleMe))

	mux.HandleFunc("GET /api/v1/system/status", s.requirePerm(auth.PermViewSystem, func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, http.StatusOK, map[string]any{
			"mode":       string(s.cfg.Mode),
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
	paperGate := func(fn func(PaperController)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Paper == nil {
				WriteError(w, http.StatusNotFound, "paper_absent", "paper engine not running (mode is not PAPER)", correlationID(r))
				return
			}
			p, _ := PrincipalFrom(r.Context())
			fn(s.Paper)
			s.log.Info("paper engine control",
				"actor", p.UserID, "path", r.URL.Path, "running", s.Paper.Running())
			WriteData(w, http.StatusOK, map[string]any{"running": s.Paper.Running()})
		}
	}
	mux.HandleFunc("POST /api/v1/paper/pause", s.requirePerm(auth.PermPaperControl, s.requireCSRF(paperGate(func(p PaperController) { p.Pause() }))))
	mux.HandleFunc("POST /api/v1/paper/resume", s.requirePerm(auth.PermPaperControl, s.requireCSRF(paperGate(func(p PaperController) { p.Resume() }))))
	mux.HandleFunc("GET /api/v1/ws", s.requireAuth(s.handleWS))
	s.configRoutes(mux)
}

func (s *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// Health probes are noise at info level.
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		s.log.Debug("http request",
			"method", r.Method, "path", r.URL.Path,
			"duration_ms", time.Since(start).Milliseconds(),
			"correlation_id", r.Header.Get("X-Correlation-ID"),
		)
	})
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
