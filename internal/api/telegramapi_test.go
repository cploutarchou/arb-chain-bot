package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTelegramStatusRoute(t *testing.T) {
	s, mux := newTestServer(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/telegram/status", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth GET = %d", rec.Code)
	}

	viewerCookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	// Never configured: 200, enabled:false — a status route reports
	// state, it does not 404 for "not configured".
	req := httptest.NewRequest(http.MethodGet, "/api/v1/telegram/status", nil)
	req.AddCookie(viewerCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("absent telegram GET = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data TelegramStatusView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Enabled {
		t.Fatalf("enabled = true without a wired provider: %+v", env.Data)
	}

	// Configured: the route serves exactly what the provider returns —
	// allowlist as ids only, no token field exists on the type at all.
	lastPoll := time.Unix(1_700_000_000, 0).UTC()
	s.Telegram = func() TelegramStatusView {
		return TelegramStatusView{
			Enabled: true, Allowlist: []int64{111, 222},
			BotUsername: "arb_ops_bot", Messages: 5, Errors: 1,
			LastPollAt: &lastPoll, LastPollOK: true,
		}
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/telegram/status", nil)
	req.AddCookie(viewerCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("configured telegram GET = %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Data.Enabled || len(env.Data.Allowlist) != 2 || env.Data.BotUsername != "arb_ops_bot" {
		t.Fatalf("configured status = %+v", env.Data)
	}
	if strings.Contains(rec.Body.String(), "token") {
		t.Fatalf("token-related field leaked into response: %s", rec.Body.String())
	}
}
