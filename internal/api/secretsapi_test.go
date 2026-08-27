package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/secrets"
)

const testSecretValue = "sk-ant-test-value-THIS-MUST-NEVER-APPEAR-9f8e7d6c"

func openTestVault(t *testing.T, env secrets.Env) *secrets.Manager {
	t.Helper()
	v, err := secrets.NewVault(secrets.NewMemoryStore(), bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return secrets.NewManager(v, "", env)
}

func doSecret(t *testing.T, mux *http.ServeMux, method, path string, cookie *http.Cookie, csrf, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestSecretsRBACAndCSRF(t *testing.T) {
	s, mux := newTestServer(t)
	s.Secrets = openTestVault(t, nil)
	body := `{"value":"` + testSecretValue + `"}`

	if rec := doSecret(t, mux, http.MethodGet, "/api/v1/secrets", nil, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth GET = %d", rec.Code)
	}
	for _, u := range []struct{ email, pw string }{{"viewer@example.test", "viewer-pw"}, {"op@example.test", "op-pw"}} {
		cookie, csrf := login(t, mux, u.email, u.pw)
		if rec := doSecret(t, mux, http.MethodPut, "/api/v1/secrets/anthropic_api_key", cookie, csrf, body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s PUT = %d: %s", u.email, rec.Code, rec.Body.String())
		}
		if rec := doSecret(t, mux, http.MethodDelete, "/api/v1/secrets/anthropic_api_key", cookie, csrf, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s DELETE = %d", u.email, rec.Code)
		}
	}
	cookie, csrf := login(t, mux, "admin@example.test", "admin-pw")
	if rec := doSecret(t, mux, http.MethodPut, "/api/v1/secrets/anthropic_api_key", cookie, "", body); rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doSecret(t, mux, http.MethodDelete, "/api/v1/secrets/anthropic_api_key", cookie, "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF delete = %d", rec.Code)
	}
	rec := doSecret(t, mux, http.MethodPut, "/api/v1/secrets/anthropic_api_key", cookie, csrf, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin PUT = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), testSecretValue) || strings.Contains(rec.Body.String(), testSecretValue[:8]) {
		t.Fatalf("PUT response leaks the value: %s", rec.Body.String())
	}
	var env struct {
		Data secrets.Info `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if !env.Data.Present || env.Data.Source != "vault" || env.Data.UpdatedBy != "u-admin" || env.Data.Applies != "immediately" || env.Data.Name != "anthropic_api_key" {
		t.Fatalf("PUT info = %+v", env.Data)
	}
	if !auth.Can(auth.RoleAdmin, auth.PermSystemConfig) {
		t.Fatal("sanity: admin must hold system:config")
	}
}

func TestSecretsUnknownAndInvalidAndVaultUnavailable(t *testing.T) {
	s, mux := newTestServer(t)
	s.Secrets = openTestVault(t, nil)
	cookie, csrf := login(t, mux, "admin@example.test", "admin-pw")
	body := `{"value":"` + testSecretValue + `"}`

	rec := doSecret(t, mux, http.MethodPut, "/api/v1/secrets/binance_api_key", cookie, csrf, body)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown_secret") {
		t.Fatalf("unknown name = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doSecret(t, mux, http.MethodDelete, "/api/v1/secrets/binance_api_key", cookie, csrf, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown delete = %d", rec.Code)
	}
	rec = doSecret(t, mux, http.MethodPut, "/api/v1/secrets/anthropic_api_key", cookie, csrf, `{"value":"short"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_secret") || strings.Contains(rec.Body.String(), "short") {
		t.Fatalf("short value = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doSecret(t, mux, http.MethodPut, "/api/v1/secrets/anthropic_api_key", cookie, csrf, `{"value":"`+testSecretValue+`","extra":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", rec.Code)
	}
	rec = doSecret(t, mux, http.MethodPut, "/api/v1/secrets/anthropic_api_key", cookie, csrf, `{"value":"`+strings.Repeat("a", 9000)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body = %d", rec.Code)
	}

	// Closed vault: 503 on writes, GET reports vault_configured:false
	// with env-backed presence.
	s.Secrets = secrets.NewManager(nil, "ARB_SECRET_KEY unset", secrets.Env{"telegram_bot_token": "env-telegram-token-value-1234"})
	rec = doSecret(t, mux, http.MethodPut, "/api/v1/secrets/anthropic_api_key", cookie, csrf, body)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "vault_unavailable") {
		t.Fatalf("closed vault PUT = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doSecret(t, mux, http.MethodGet, "/api/v1/secrets", cookie, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d", rec.Code)
	}
	var env struct {
		Data struct {
			VaultConfigured bool           `json:"vault_configured"`
			Reason          string         `json:"reason"`
			KeyID           string         `json:"key_id"`
			Secrets         []secrets.Info `json:"secrets"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.VaultConfigured || env.Data.Reason == "" || env.Data.KeyID != "" || len(env.Data.Secrets) != 2 {
		t.Fatalf("closed vault GET = %+v", env.Data)
	}
	if strings.Contains(rec.Body.String(), "env-telegram-token") {
		t.Fatalf("GET leaks an env value: %s", rec.Body.String())
	}
	for _, in := range env.Data.Secrets {
		if in.Name == "telegram_bot_token" && (!in.Present || in.Source != "env" || in.Applies != "process_restart") {
			t.Fatalf("telegram info = %+v", in)
		}
	}

	// Profile without a vault at all.
	s.Secrets = nil
	if rec := doSecret(t, mux, http.MethodPut, "/api/v1/secrets/anthropic_api_key", cookie, csrf, body); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no vault PUT = %d", rec.Code)
	}
}

// TestSecretPutNeverLogsValue is the design §7 log-capture assertion:
// the submitted value appears in no emitted record for PUT (the
// request-logging middleware is wrapped in too, at debug level, since
// it is not assumed body-safe), nor in the GET afterwards.
func TestSecretPutNeverLogsValue(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := config.Bootstrap{Mode: config.ModeMarketData, HTTPAddr: ":0"}
	s := NewServer(cfg, logger, BuildInfo{Version: "test"})
	store := auth.NewMemoryStore()
	hash, _ := auth.HashPassword("admin-pw")
	store.AddUser(auth.User{ID: "u-admin", Email: "admin@example.test", PasswordHash: hash, Role: auth.RoleAdmin})
	s.Auth = &auth.Manager{Users: store, Sessions: store, Throttle: auth.NewThrottle(10, time.Minute, time.Minute), TTL: time.Hour, Now: time.Now}
	s.Secrets = openTestVault(t, nil)
	s.AuditAction = func(actor, action, entity, ip, correlationID string) {
		logger.Info("audit", "actor", actor, "action", action, "entity", entity)
	}
	inner := http.NewServeMux()
	s.routes(inner)
	handler := s.withRequestLog(inner)
	mux := http.NewServeMux()
	mux.Handle("/", handler)

	cookie, csrf := login(t, mux, "admin@example.test", "admin-pw")
	rec := doSecret(t, mux, http.MethodPut, "/api/v1/secrets/telegram_bot_token", cookie, csrf, `{"value":"  `+testSecretValue+`\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doSecret(t, mux, http.MethodGet, "/api/v1/secrets", cookie, "", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), testSecretValue) {
		t.Fatalf("GET = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = doSecret(t, mux, http.MethodDelete, "/api/v1/secrets/telegram_bot_token", cookie, csrf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", rec.Code, rec.Body.String())
	}
	out := logs.String()
	if !strings.Contains(out, "secret.write") || !strings.Contains(out, "secret.delete") {
		t.Fatalf("mutations not audited: %s", out)
	}
	for _, needle := range []string{testSecretValue, testSecretValue[:12], testSecretValue[len(testSecretValue)-4:]} {
		if strings.Contains(out, needle) {
			t.Fatalf("secret value (or a fragment) leaked into logs: %s", out)
		}
	}
	// The value was trimmed, stored, and resolves through the manager.
	if v, src, ok := s.Secrets.(*secrets.Manager).Get(t.Context(), "telegram_bot_token"); ok || v != "" || src != "" {
		t.Fatalf("after delete the vault must be empty: %q %q %v", v, src, ok)
	}
}

func TestCapabilitiesShapeAndNewFieldTiming(t *testing.T) {
	s, mux, _ := newPlatformServer(t)
	s.Secrets = openTestVault(t, nil)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	rec := doSecret(t, mux, http.MethodGet, "/api/v1/platform/capabilities", cookie, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("capabilities = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Modes []struct {
				ID        string `json:"id"`
				Available bool   `json:"available"`
				Reason    string `json:"reason"`
			} `json:"modes"`
			Venues []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Available bool   `json:"available"`
				Reason    string `json:"reason"`
			} `json:"venues"`
			AIProviders []struct {
				ID        string `json:"id"`
				Available bool   `json:"available"`
				Reason    string `json:"reason"`
			} `json:"ai_providers"`
			LogLevels   []string          `json:"log_levels"`
			Secrets     map[string]any    `json:"secrets"`
			FieldTiming map[string]string `json:"field_timing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	d := env.Data
	modes := map[string]bool{}
	for _, m := range d.Modes {
		modes[m.ID] = m.Available
		if m.ID == "SHADOW" && (m.Available || m.Reason == "") {
			t.Fatalf("SHADOW must be listed unavailable with a reason: %+v", m)
		}
	}
	if !modes["MARKET_DATA"] || !modes["RECORD"] || !modes["PAPER"] || len(modes) != 4 {
		t.Fatalf("modes = %v", modes)
	}
	if _, ok := modes["LIVE"]; ok {
		t.Fatal("LIVE must never be enumerated")
	}
	venues := map[string]bool{}
	for _, v := range d.Venues {
		venues[v.ID] = v.Available
		if !v.Available && !strings.Contains(v.Reason, "T-05") {
			t.Fatalf("venue %s reason = %q", v.ID, v.Reason)
		}
	}
	if !venues["binance"] || venues["okx"] || len(venues) != 6 {
		t.Fatalf("venues = %v", venues)
	}
	providers := map[string]bool{}
	for _, p := range d.AIProviders {
		providers[p.ID] = p.Available
		if p.ID == "openai" && (p.Available || p.Reason != "provider not built") {
			t.Fatalf("openai = %+v", p)
		}
	}
	if !providers["anthropic"] || !providers["fake"] || len(providers) != 3 {
		t.Fatalf("providers = %v", providers)
	}
	if len(d.LogLevels) != 4 || d.Secrets["vault_configured"] != true || d.Secrets["key_id"] == "" {
		t.Fatalf("log_levels=%v secrets=%v", d.LogLevels, d.Secrets)
	}
	for path, want := range map[string]string{
		"platform.mode": "restart", "platform.log_level": "hot", "platform.allowed_origin": "hot",
		"ai.enabled": "hot", "ai.budget.max_output_tokens": "hot", "telegram.disabled": "hot",
		"venues.binance.symbols": "restart",
	} {
		if d.FieldTiming[path] != want {
			t.Fatalf("field_timing[%s] = %q, want %q", path, d.FieldTiming[path], want)
		}
	}

	// /platform/venues is an alias over the same table (additive fields).
	rec = doSecret(t, mux, http.MethodGet, "/api/v1/platform/venues", cookie, "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"okx"`) || !strings.Contains(rec.Body.String(), `"pay_asset":"BNB"`) {
		t.Fatalf("venues alias = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPlatformApplyRejectsUnbuiltVenueAndWarnsOnIdleAdvisor(t *testing.T) {
	s, mux, _ := newPlatformServer(t)
	cookie, csrf := login(t, mux, "admin@example.test", "admin-pw")

	doc := platformTestSettings()
	doc.Venues["okx"] = doc.Venues["binance"]
	rec := postPlatform(t, mux, cookie, csrf, "/api/v1/platform/settings", map[string]any{"settings": doc, "parent_version": 1})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "connector_unavailable") || !strings.Contains(rec.Body.String(), "T-050") {
		t.Fatalf("okx apply = %d: %s", rec.Code, rec.Body.String())
	}

	// LIVE is refused by name through the API too.
	doc = platformTestSettings()
	doc.Platform.Mode = "LIVE"
	rec = postPlatform(t, mux, cookie, csrf, "/api/v1/platform/settings", map[string]any{"settings": doc, "parent_version": 1})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "live trading is permanently disabled") {
		t.Fatalf("LIVE apply = %d: %s", rec.Code, rec.Body.String())
	}

	// Enabled anthropic with no key: accepted, with a warning naming the
	// reason the status hook reports.
	s.AIStatus = func() AIRuntimeStatus {
		return AIRuntimeStatus{Enabled: true, Running: false, Reason: "no anthropic_api_key", Provider: "anthropic"}
	}
	doc = platformTestSettings()
	doc.AI.Enabled = true
	rec = postPlatform(t, mux, cookie, csrf, "/api/v1/platform/settings", map[string]any{"settings": doc, "parent_version": 1})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"warnings":["ai.enabled is true but the advisor is not running: no anthropic_api_key"]`) {
		t.Fatalf("ai apply = %d: %s", rec.Code, rec.Body.String())
	}

	rec = doSecret(t, mux, http.MethodGet, "/api/v1/ai/status", cookie, "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"running":false`) || !strings.Contains(rec.Body.String(), "no anthropic_api_key") {
		t.Fatalf("ai status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSystemStatusModeAndAllowedOriginAreHot(t *testing.T) {
	s, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	rec := doSecret(t, mux, http.MethodGet, "/api/v1/system/status", cookie, "", "")
	if !strings.Contains(rec.Body.String(), `"mode":"MARKET_DATA"`) {
		t.Fatalf("default mode from cfg: %s", rec.Body.String())
	}
	s.Mode = func() string { return "PAPER" }
	rec = doSecret(t, mux, http.MethodGet, "/api/v1/system/status", cookie, "", "")
	if !strings.Contains(rec.Body.String(), `"mode":"PAPER"`) {
		t.Fatalf("injected mode: %s", rec.Body.String())
	}
	if s.AllowedOrigin() != "" {
		t.Fatalf("origin seeded from cfg = %q", s.AllowedOrigin())
	}
	s.SetAllowedOrigin("https://console.example.com")
	if s.AllowedOrigin() != "https://console.example.com" {
		t.Fatal("SetAllowedOrigin not observed")
	}
}
