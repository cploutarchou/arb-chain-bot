package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
)

func platformTestSettings() platform.Settings {
	// T-059: the fixture predates the platform/ai sections; WithDefaults
	// fills them from a PAPER seed exactly as Service.Load would.
	return platform.Settings{
		Venues: map[string]platform.VenueSettings{
			"binance": {
				Enabled: true, PaperEnabled: true,
				Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
				StartingAssets: []string{"USDT"},
				Fees: platform.FeeSettings{
					MakerBps: decimal.NewFromInt(10),
					TakerBps: decimal.NewFromInt(10),
				},
			},
		},
		Paper:    platform.PaperSettings{Balances: map[string]string{"USDT": "10000"}},
		Telegram: platform.TelegramSettings{Allowlist: []int64{111}},
	}.WithDefaults(config.Bootstrap{Mode: config.ModePaper, AIModel: "claude-sonnet-5"})
}

func newPlatformServer(t *testing.T) (*Server, *http.ServeMux, *platform.Service) {
	t.Helper()
	s, _ := newTestServer(t)
	svc := platform.NewService(platform.NewMemoryStore(), discardLogger(), nil)
	cfg := config.Bootstrap{
		Mode: config.ModePaper, Symbols: []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
		StartingAssets: []string{"USDT"}, PaperBalance: "10000",
	}
	if _, err := svc.Load(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	s.Platform = svc
	mux2 := http.NewServeMux()
	s.routes(mux2)
	return s, mux2, svc
}

func postPlatform(t *testing.T, mux *http.ServeMux, cookie *http.Cookie, csrf, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	req.AddCookie(cookie)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPlatformReadRequiresAuthAndReturnsFieldTiming(t *testing.T) {
	_, mux, _ := newPlatformServer(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/settings", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth GET = %d", rec.Code)
	}

	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/settings", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Version     int64             `json:"version"`
			FieldTiming map[string]string `json:"field_timing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Version != 1 {
		t.Fatalf("version = %d, want 1", env.Data.Version)
	}
	if got := env.Data.FieldTiming["telegram.allowlist"]; got != "restart" {
		t.Fatalf("telegram.allowlist timing = %q, want restart (no BotRunning wired)", got)
	}
	if got := env.Data.FieldTiming["venues.binance.symbols"]; got != "restart" {
		t.Fatalf("venues.binance.symbols timing = %q, want restart", got)
	}
}

func TestPlatformWritePermissionsBySection(t *testing.T) {
	_, mux, svc := newPlatformServer(t)
	next := platformTestSettings()
	next.Venues["binance"] = platform.VenueSettings{
		Enabled: true, PaperEnabled: true,
		Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC", "BNBUSDT"},
		StartingAssets: []string{"USDT"},
		Fees:           next.Venues["binance"].Fees,
	}

	vCookie, vCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	if rec := postPlatform(t, mux, vCookie, vCSRF, "/api/v1/platform/settings", map[string]any{"settings": next, "parent_version": 1}); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer write = %d: %s", rec.Code, rec.Body.String())
	}

	// Operator holds neither exchange:config nor system:config — both
	// platform-settings permissions are ADMIN-only in the matrix.
	oCookie, oCSRF := login(t, mux, "op@example.test", "op-pw")
	if rec := postPlatform(t, mux, oCookie, oCSRF, "/api/v1/platform/settings", map[string]any{"settings": next, "parent_version": 1}); rec.Code != http.StatusForbidden {
		t.Fatalf("operator write = %d: %s", rec.Code, rec.Body.String())
	}

	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")
	if rec := postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings", map[string]any{"settings": next, "parent_version": 1}); rec.Code != http.StatusOK {
		t.Fatalf("admin write = %d: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version after admin write = %d, want 2", got)
	}

	// Identical payload → 400 no_change (design diverges from configapi's 409).
	rec := postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings", map[string]any{"settings": next, "parent_version": 2})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no_change") {
		t.Fatalf("no-change write = %d: %s", rec.Code, rec.Body.String())
	}
}

// TestPlatformApplyRollbackRequireParentVersionForWeb is the review
// P3(g) regression: a web-sourced platform-settings apply/rollback that
// omits parent_version entirely must fail with 400
// parent_version_required, not silently skip the optimistic-concurrency
// check.
func TestPlatformApplyRollbackRequireParentVersionForWeb(t *testing.T) {
	_, mux, svc := newPlatformServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	next := svc.Current().Settings.Clone()
	v := next.Venues["binance"]
	v.Fees.TakerBps = decimal.NewFromInt(7)
	next.Venues["binance"] = v

	rec := postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings", map[string]any{"settings": next})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "parent_version_required") {
		t.Fatalf("apply without parent_version = %d: %s, want 400 parent_version_required", rec.Code, rec.Body.String())
	}

	rec = postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings/rollback", map[string]int64{"version": 1})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "parent_version_required") {
		t.Fatalf("rollback without parent_version = %d: %s, want 400 parent_version_required", rec.Code, rec.Body.String())
	}
}

func TestPlatformApplyOptimisticConcurrency(t *testing.T) {
	_, mux, svc := newPlatformServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	next := svc.Current().Settings.Clone()
	v := next.Venues["binance"]
	v.Fees.TakerBps = decimal.NewFromInt(7)
	next.Venues["binance"] = v

	// Correct parent_version (matches the loaded version 1) succeeds.
	rec := postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings",
		map[string]any{"settings": next, "parent_version": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply with correct parent_version = %d: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version after apply = %d, want 2", got)
	}

	// Stale parent_version (still says 1) refused with 409 stale_version,
	// current version reported in the body — even though the payload is
	// unchanged from the just-applied version (which would otherwise be
	// a 400 no_change): the concurrency error takes priority.
	rec = postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings",
		map[string]any{"settings": next, "parent_version": 1})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale apply = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data  map[string]int64 `json:"data"`
		Error *APIError        `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Code != "stale_version" {
		t.Fatalf("error = %+v, want stale_version", env.Error)
	}
	if env.Data["current_version"] != 2 {
		t.Fatalf("current_version = %d, want 2", env.Data["current_version"])
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version must not advance on a stale write, got %d", got)
	}

	// review P3(g): omitting parent_version entirely is now a hard 400
	// (TestPlatformApplyRollbackRequireParentVersionForWeb covers this in
	// isolation) rather than the old unchecked-apply fallthrough.
	next2 := svc.Current().Settings.Clone()
	v2 := next2.Venues["binance"]
	v2.Fees.TakerBps = decimal.NewFromInt(9)
	next2.Venues["binance"] = v2
	rec = postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings", map[string]any{"settings": next2})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("apply without parent_version = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version must not advance on a rejected apply, got %d", got)
	}
}

func TestPlatformRollbackOptimisticConcurrency(t *testing.T) {
	_, mux, svc := newPlatformServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	next := svc.Current().Settings.Clone()
	v := next.Venues["binance"]
	v.Fees.TakerBps = decimal.NewFromInt(7)
	next.Venues["binance"] = v
	if rec := postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings", map[string]any{"settings": next, "parent_version": 1}); rec.Code != http.StatusOK {
		t.Fatalf("seed write = %d", rec.Code)
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version = %d, want 2", got)
	}

	rec := postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings/rollback",
		map[string]any{"version": 1, "parent_version": 1})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale rollback = %d: %s", rec.Code, rec.Body.String())
	}

	rec = postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings/rollback",
		map[string]any{"version": 1, "parent_version": 2})
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback with correct parent_version = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPlatformWriteCSRFRequired(t *testing.T) {
	_, mux, _ := newPlatformServer(t)
	aCookie, _ := login(t, mux, "admin@example.test", "admin-pw")
	rec := postPlatform(t, mux, aCookie, "", "/api/v1/platform/settings", map[string]any{"settings": platformTestSettings()})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("write without CSRF = %d, want 403", rec.Code)
	}
}

func TestPlatformPreviewDoesNotWrite(t *testing.T) {
	_, mux, svc := newPlatformServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	// Based on the CURRENT snapshot, not an independent fixture: only
	// the one field mutated below may differ, so "sections" is exact.
	next := svc.Current().Settings.Clone()
	v := next.Venues["binance"]
	v.Fees.TakerBps = decimal.NewFromInt(7)
	next.Venues["binance"] = v

	rec := postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings/preview", map[string]any{"settings": next})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Sections        []string `json:"sections"`
			RequiresRestart bool     `json:"requires_restart"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Data.RequiresRestart {
		t.Fatal("a fee change must be reported as restart-scoped")
	}
	if len(env.Data.Sections) != 1 || env.Data.Sections[0] != "venues" {
		t.Fatalf("sections = %v, want [venues]", env.Data.Sections)
	}
	if got := svc.Current().Version; got != 1 {
		t.Fatalf("preview must not write a new version, got version %d", got)
	}
}

func TestPlatformRollback(t *testing.T) {
	_, mux, svc := newPlatformServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	next := platformTestSettings()
	v := next.Venues["binance"]
	v.Fees.TakerBps = decimal.NewFromInt(7)
	next.Venues["binance"] = v
	if rec := postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings", map[string]any{"settings": next, "parent_version": 1}); rec.Code != http.StatusOK {
		t.Fatalf("write = %d: %s", rec.Code, rec.Body.String())
	}

	if rec := postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings/rollback", map[string]any{"version": 1, "parent_version": 2}); rec.Code != http.StatusOK {
		t.Fatalf("rollback = %d: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Version; got != 3 {
		t.Fatalf("rollback version = %d, want 3 (append-only)", got)
	}

	if rec := postPlatform(t, mux, aCookie, aCSRF, "/api/v1/platform/settings/rollback", map[string]any{"version": 99, "parent_version": 3}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing version rollback = %d", rec.Code)
	}
}

// TestPlatformVenuesRequiresAuthAndIsHonestAboutModeled is requirement
// (a)'s handler test: GET /api/v1/platform/venues serves the compiled
// discount table (asset/rate/applies_to_api) so the console never
// hardcodes it, and every entry must report modeled:false (P1-2) so the
// console can disable the toggle instead of offering a control that
// always 400s on apply. It works even with no platform.Service wired
// (unlike /platform/settings) since the table is a static compile-time
// constant, not a Settings read.
func TestPlatformVenuesRequiresAuthAndIsHonestAboutModeled(t *testing.T) {
	_, mux := newTestServer(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/venues", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth GET = %d", rec.Code)
	}

	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/venues", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Venues []struct {
				ID       string `json:"id"`
				Discount *struct {
					PayAsset     string `json:"pay_asset"`
					Rate         string `json:"rate"`
					AppliesToAPI bool   `json:"applies_to_api"`
					Modeled      bool   `json:"modeled"`
					Reason       string `json:"reason"`
				} `json:"discount"`
			} `json:"venues"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var sawBinance bool
	for _, v := range env.Data.Venues {
		if v.ID != "binance" {
			continue
		}
		sawBinance = true
		if v.Discount == nil {
			t.Fatal("binance must report its compiled-in discount profile")
		}
		if v.Discount.Modeled {
			t.Fatal("modeled must be false until a pay-asset ledger exists (P1-2)")
		}
		if v.Discount.PayAsset != "BNB" || !v.Discount.AppliesToAPI || v.Discount.Reason == "" {
			t.Fatalf("unexpected discount payload: %+v", v.Discount)
		}
	}
	if !sawBinance {
		t.Fatalf("expected binance in the response: %+v", env.Data.Venues)
	}
}

func TestPlatformAbsentServiceIs503(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/settings", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("absent service GET = %d, want 503", rec.Code)
	}
}

// --- engine restart routes -------------------------------------------

type fakeRestart struct {
	status                RestartStatus
	result                RestartRequestResult
	calls                 int
	lastActor, lastReason string
	lastStopRecording     bool
}

func (f *fakeRestart) Request(actor, reason string, stopRecording bool) RestartRequestResult {
	f.calls++
	f.lastActor, f.lastReason, f.lastStopRecording = actor, reason, stopRecording
	return f.result
}

func (f *fakeRestart) Status() RestartStatus { return f.status }

func TestEngineStatusAbsentIs404(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	for _, path := range []string{"/api/v1/engine/status", "/api/v1/engine/restart"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s absent = %d, want 404", path, rec.Code)
		}
	}
}

func TestEngineStatusBothRoutesServeSameShape(t *testing.T) {
	s, mux := newTestServer(t)
	fr := &fakeRestart{status: RestartStatus{State: "ready", SettingsVersion: 3, Restarts: 2}}
	s.Restart = fr
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	for _, path := range []string{"/api/v1/engine/status", "/api/v1/engine/restart"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
		}
		var env struct {
			Data struct {
				Restart RestartStatus `json:"restart"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.Restart.SettingsVersion != 3 || env.Data.Restart.Restarts != 2 {
			t.Fatalf("%s payload = %+v", path, env.Data.Restart)
		}
	}
}

func TestEngineRestartRBACAndCSRF(t *testing.T) {
	s, mux := newTestServer(t)
	fr := &fakeRestart{result: RestartRequestResult{Accepted: true}}
	s.Restart = fr

	body := `{"confirm":"RESTART"}`
	vCookie, vCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/engine/restart", strings.NewReader(body))
	req.AddCookie(vCookie)
	req.Header.Set("X-CSRF-Token", vCSRF)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer restart = %d, want 403", rec.Code)
	}

	oCookie, oCSRF := login(t, mux, "op@example.test", "op-pw")
	req = httptest.NewRequest(http.MethodPost, "/api/v1/engine/restart", strings.NewReader(body))
	req.AddCookie(oCookie)
	req.Header.Set("X-CSRF-Token", oCSRF)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("operator restart = %d, want 403 (system:config is ADMIN-only)", rec.Code)
	}

	aCookie, _ := login(t, mux, "admin@example.test", "admin-pw")
	req = httptest.NewRequest(http.MethodPost, "/api/v1/engine/restart", strings.NewReader(body))
	req.AddCookie(aCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin restart without CSRF = %d, want 403", rec.Code)
	}
}

func TestEngineRestartConfirmRequired(t *testing.T) {
	s, mux := newTestServer(t)
	fr := &fakeRestart{result: RestartRequestResult{Accepted: true}}
	s.Restart = fr
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	for _, body := range []string{`{}`, `{"confirm":"restart"}`, `{"confirm":"RESET"}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/engine/restart", strings.NewReader(body))
		req.AddCookie(aCookie)
		req.Header.Set("X-CSRF-Token", aCSRF)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "confirm_required") {
			t.Fatalf("body %s: restart = %d %s, want 400 confirm_required", body, rec.Code, rec.Body.String())
		}
	}
	if fr.calls != 0 {
		t.Fatalf("Request must not be called without a valid confirm token, got %d calls", fr.calls)
	}
}

func TestEngineRestartGuardRail409(t *testing.T) {
	s, mux := newTestServer(t)
	fr := &fakeRestart{result: RestartRequestResult{Accepted: false, Code: "campaign_running", Message: "campaign run camp-1 is in progress"}}
	s.Restart = fr
	var audited []string
	s.AuditAction = func(actor, action, entity, ip, correlationID string) {
		audited = append(audited, action+":"+entity)
	}
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/engine/restart", strings.NewReader(`{"confirm":"RESTART"}`))
	req.AddCookie(aCookie)
	req.Header.Set("X-CSRF-Token", aCSRF)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "campaign_running") {
		t.Fatalf("guard-rail refusal = %d: %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, a := range audited {
		if a == "engine.restart.refused:engine:campaign_running" {
			found = true
		}
	}
	if !found {
		t.Fatalf("refusal not audited: %v", audited)
	}
}

func TestEngineRestartSuccess202(t *testing.T) {
	s, mux := newTestServer(t)
	fr := &fakeRestart{
		result: RestartRequestResult{Accepted: true},
		status: RestartStatus{State: "restarting"},
	}
	s.Restart = fr
	var audited []string
	s.AuditAction = func(actor, action, entity, ip, correlationID string) {
		audited = append(audited, action)
	}
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/engine/restart",
		strings.NewReader(`{"confirm":"RESTART","reason":"apply v8","stop_recording":true}`))
	req.AddCookie(aCookie)
	req.Header.Set("X-CSRF-Token", aCSRF)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("restart = %d: %s", rec.Code, rec.Body.String())
	}
	if fr.calls != 1 || fr.lastReason != "apply v8" || !fr.lastStopRecording {
		t.Fatalf("Request not called with the expected args: calls=%d reason=%q stop=%v", fr.calls, fr.lastReason, fr.lastStopRecording)
	}
	// P3-9: the API layer audits the REQUEST as "engine.restart.requested"
	// (fired here, the moment it's accepted) — the run's own completion
	// is a separate "engine.restart.completed" event from the supervisor,
	// not exercised by this fake.
	found := false
	for _, a := range audited {
		if a == "engine.restart.requested" {
			found = true
		}
	}
	if !found {
		t.Fatalf("success not audited: %v", audited)
	}
}
