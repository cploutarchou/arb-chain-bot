package tracing

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// Disabled tracing (no endpoint) must install cleanly, start no-op
// spans cheaply and report disabled — the platform's default state.
func TestDisabledByDefault(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if Enabled() {
		t.Fatal("enabled without an endpoint")
	}
	ctx, span := Start(context.Background(), "test.span")
	if span == nil || ctx == nil {
		t.Fatal("noop start returned nils")
	}
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
