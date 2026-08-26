package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
)

func newTestServer(t *testing.T) *http.ServeMux {
	t.Helper()
	cfg := config.Bootstrap{Mode: config.ModeMarketData, HTTPAddr: ":0"}
	s := NewServer(cfg, discardLogger(), BuildInfo{Version: "test", Components: []string{"api"}})
	mux := http.NewServeMux()
	s.routes(mux)
	return mux
}

func TestHealthz(t *testing.T) {
	mux := newTestServer(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
}

func TestSystemStatusEnvelope(t *testing.T) {
	mux := newTestServer(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var env Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error != nil {
		t.Fatalf("unexpected error: %+v", env.Error)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type %T", env.Data)
	}
	if data["mode"] != "MARKET_DATA" {
		t.Fatalf("mode = %v", data["mode"])
	}
}
