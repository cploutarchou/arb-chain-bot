package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Server serves /metrics on its own listener when ARB_METRICS_ADDR is
// set (production posture: scrape port stays off the public API mux).
type Server struct {
	Addr    string
	Handler http.Handler
	Log     *slog.Logger
}

func (s *Server) Name() string { return "metrics" }

func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", s.Handler)
	srv := &http.Server{Addr: s.Addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	errCh := make(chan error, 1)
	go func() {
		s.Log.Info("metrics listening", "addr", s.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}
