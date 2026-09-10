package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/secrets"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// newTenantServer builds a server with an in-memory tenancy store and
// resolver: the platform admin (org 1), and two tenant organisations
// A (Signal, acknowledged) and B (Watch, not acknowledged), each with
// an OWNER, plus a VIEWER in A. Every tenant user holds the console
// ADMIN role so that RBAC alone would let them through — the tests
// below prove tenancy gates what RBAC cannot.
func newTenantServer(t *testing.T) (*Server, *http.ServeMux, *tenancy.MemoryStore, *entitlements.MapSource) {
	t.Helper()
	s, _ := newTestServer(t)
	users := auth.NewMemoryStore()
	add := func(id, email, pw string, role auth.Role, platform bool) {
		hash, err := auth.HashPassword(pw)
		if err != nil {
			t.Fatal(err)
		}
		users.AddUser(auth.User{ID: id, Email: email, PasswordHash: hash, Role: role, PlatformAdmin: platform, CreatedAt: time.Now()})
	}
	add("u-admin", "admin@example.test", "admin-pw", auth.RoleAdmin, true)
	add("u-staff", "staff@example.test", "staff-pw", auth.RoleAdmin, false)
	add("a-owner", "owner@a.test", "a-owner-pw", auth.RoleAdmin, false)
	add("a-viewer", "viewer@a.test", "a-viewer-pw", auth.RoleAdmin, false)
	add("b-owner", "owner@b.test", "b-owner-pw", auth.RoleAdmin, false)
	add("nobody", "nobody@x.test", "nobody-pw", auth.RoleAdmin, false)
	s.Auth = &auth.Manager{Users: users, Sessions: users, TTL: time.Hour, Now: time.Now}
	s.Users = &auth.AdminService{Store: users, Sessions: users, Now: time.Now}

	ts := tenancy.NewMemoryStore()
	_ = ts.AddMember(context.Background(), tenancy.Membership{OrgID: 1, UserID: "u-admin", Role: tenancy.RoleOwner})
	_ = ts.AddMember(context.Background(), tenancy.Membership{OrgID: 1, UserID: "u-staff", Role: tenancy.RoleAdmin})
	orgA, _ := ts.CreateOrg(context.Background(), tenancy.Org{Name: "A", PackageCode: "signal", RiskAckVersion: "2026-08-27"}, "a-owner")
	_ = ts.AddMember(context.Background(), tenancy.Membership{OrgID: orgA.ID, UserID: "a-viewer", Role: tenancy.RoleViewer})
	_, _ = ts.CreateOrg(context.Background(), tenancy.Org{Name: "B", PackageCode: "watch", RiskAckVersion: "2026-08-27"}, "b-owner")
	src := entitlements.NewMapSource()
	src.Set(entitlements.Input{OrgID: 1, PackageCode: "institution"})
	src.Set(entitlements.Input{OrgID: 2, PackageCode: "signal", SubStatus: "active"})
	src.Set(entitlements.Input{OrgID: 3, PackageCode: "watch"})
	s.Tenancy = ts
	s.Entitlements = entitlements.NewResolver(src)
	s.RiskAckVersion = "2026-08-27"

	svc := screener.NewService(screener.NewBook(), screener.NewMemoryStore(), discardLogger(), nil)
	if _, err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.Rules = screener.NewMemoryRuleStore()
	svc.Events = screener.NewMemoryEventStore()
	svc.Templates = screener.NewMemoryTemplateStore()
	svc.Funding = screener.NewMemoryFundingStore()
	s.Screener = svc
	s.Secrets = openTestVault(t, nil)
	mux := http.NewServeMux()
	s.routes(mux)
	return s, mux, ts, src
}

func call(t *testing.T, mux *http.ServeMux, method, path string, cookie *http.Cookie, csrf string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	switch b := body.(type) {
	case nil:
		rdr = strings.NewReader("")
	case string:
		rdr = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rdr = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "203.0.113.9:4444"
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

func envelope(t *testing.T, rec *httptest.ResponseRecorder) (map[string]any, *APIError) {
	t.Helper()
	var env struct {
		Data  map[string]any `json:"data"`
		Error *APIError      `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("envelope: %v: %s", err, rec.Body.String())
	}
	return env.Data, env.Error
}

// TestVaultExchangeGroupIsPlatformAdminOnly is compliance review #1,
// tightened by audit S8: the ENTIRE vault — inventory and writes, not
// just the exchange group — is the platform operator's. Tenant OWNER/
// ADMIN (console ADMIN role, so role RBAC alone would allow it) and
// platform staff without the flag are refused everywhere; the platform
// admin keeps full access. Before S8 a promoted console ADMIN could
// overwrite the Paddle webhook secret and forge subscription events.
func TestVaultExchangeGroupIsPlatformAdminOnly(t *testing.T) {
	_, mux, _, _ := newTenantServer(t)
	body := `{"value":"read-only-key-0123456789abcdef"}`
	for _, u := range []struct{ email, pw string }{{"owner@a.test", "a-owner-pw"}, {"owner@b.test", "b-owner-pw"}, {"staff@example.test", "staff-pw"}} {
		cookie, csrf := login(t, mux, u.email, u.pw)
		for name := range secrets.Known {
			if secrets.Known[name].Group != secrets.GroupExchange {
				continue
			}
			rec := call(t, mux, http.MethodPut, "/api/v1/secrets/"+name, cookie, csrf, body)
			if _, e := envelope(t, rec); rec.Code != http.StatusForbidden || e == nil || e.Code != "platform_admin_required" {
				t.Fatalf("%s PUT %s = %d %s", u.email, name, rec.Code, rec.Body.String())
			}
			rec = call(t, mux, http.MethodDelete, "/api/v1/secrets/"+name, cookie, csrf, nil)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s DELETE %s = %d", u.email, name, rec.Code)
			}
		}
		// S8: the inventory and the PROVIDER entries are operator-only
		// too — a tenant seat never learns what the vault contains.
		rec := call(t, mux, http.MethodGet, "/api/v1/secrets", cookie, "", nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s GET = %d", u.email, rec.Code)
		}
		if rec := call(t, mux, http.MethodPut, "/api/v1/secrets/anthropic_api_key", cookie, csrf, body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s provider PUT = %d", u.email, rec.Code)
		}
	}
	cookie, csrf := login(t, mux, "admin@example.test", "admin-pw")
	rec := call(t, mux, http.MethodPut, "/api/v1/secrets/binance_api_key", cookie, csrf, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("platform admin PUT = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, mux, http.MethodGet, "/api/v1/secrets", cookie, "", nil)
	if !strings.Contains(rec.Body.String(), `"binance_api_key"`) {
		t.Fatal("platform admin does not see the exchange group")
	}
	// Unknown names still 404 for everyone (no registry oracle).
	if rec := call(t, mux, http.MethodPut, "/api/v1/secrets/kraken_api_key", cookie, csrf, body); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d", rec.Code)
	}
}

// TestSystemRoutesArePlatformAdminOnly: users & roles, platform
// settings, engine restart and price mapping refuse tenant admins.
func TestSystemRoutesArePlatformAdminOnly(t *testing.T) {
	_, mux, _, _ := newTenantServer(t)
	cookie, csrf := login(t, mux, "owner@a.test", "a-owner-pw")
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/users"},
		{http.MethodPost, "/api/v1/users"},
		{http.MethodPost, "/api/v1/platform/settings"},
		{http.MethodPost, "/api/v1/platform/settings/rollback"},
		{http.MethodPost, "/api/v1/orgs"},
		{http.MethodPut, "/api/v1/orgs/2/override"},
		{http.MethodPut, "/api/v1/billing/prices/pri_x"},
	} {
		rec := call(t, mux, rt.method, rt.path, cookie, csrf, `{}`)
		if _, e := envelope(t, rec); rec.Code != http.StatusForbidden || e == nil || e.Code != "platform_admin_required" {
			t.Fatalf("%s %s = %d %s", rt.method, rt.path, rec.Code, rec.Body.String())
		}
	}
}

// TestMeAndRiskAck: /me reports org, role and entitlements; an
// organisation that has not acknowledged the current disclosure gets
// 403 risk_ack_required on protected routes (but not on /me or the
// acknowledgement itself) until POST /me/risk-ack stores version, time
// and IP.
func TestMeAndRiskAck(t *testing.T) {
	_, mux, ts, _ := newTenantServer(t)
	_ = ts.SetRiskAck(context.Background(), 3, "", time.Time{}, "")
	cookie, csrf := login(t, mux, "owner@b.test", "b-owner-pw")

	rec := call(t, mux, http.MethodGet, "/api/v1/me", cookie, "", nil)
	data, _ := envelope(t, rec)
	if rec.Code != http.StatusOK || data["risk_ack_required"] != true || data["org_role"] != "OWNER" || data["platform_admin"] != false {
		t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
	}
	ent := data["entitlements"].(map[string]any)
	if ent["package_code"] != "watch" || ent["execution"].(map[string]any)["live"] != false {
		t.Fatalf("entitlements = %v", ent)
	}
	rec = call(t, mux, http.MethodGet, "/api/v1/screener/rules", cookie, "", nil)
	if _, e := envelope(t, rec); rec.Code != http.StatusForbidden || e == nil || e.Code != "risk_ack_required" {
		t.Fatalf("pre-ack rules = %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, mux, http.MethodPost, "/api/v1/me/risk-ack", cookie, csrf, `{"version":"2025-01-01"}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale version = %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, mux, http.MethodPost, "/api/v1/me/risk-ack", cookie, "", `{"version":"2026-08-27"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("ack without CSRF = %d", rec.Code)
	}
	if rec := call(t, mux, http.MethodPost, "/api/v1/me/risk-ack", cookie, csrf, `{"version":"2026-08-27"}`); rec.Code != http.StatusOK {
		t.Fatalf("ack = %d %s", rec.Code, rec.Body.String())
	}
	org, _ := ts.Org(context.Background(), 3)
	if org.RiskAckVersion != "2026-08-27" || org.RiskAckAt == nil || org.RiskAckIP != "203.0.113.9" {
		t.Fatalf("stored ack = %+v", org)
	}
	if rec := call(t, mux, http.MethodGet, "/api/v1/screener/rules", cookie, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("post-ack rules = %d %s", rec.Code, rec.Body.String())
	}
	// Platform staff are never gated.
	_ = ts.SetRiskAck(context.Background(), 1, "", time.Time{}, "")
	acookie, _ := login(t, mux, "admin@example.test", "admin-pw")
	if rec := call(t, mux, http.MethodGet, "/api/v1/screener/rules", acookie, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("platform admin gated = %d", rec.Code)
	}
	// No membership at all: 403 no_organisation.
	ncookie, _ := login(t, mux, "nobody@x.test", "nobody-pw")
	if rec := call(t, mux, http.MethodGet, "/api/v1/me", ncookie, "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("no membership = %d %s", rec.Code, rec.Body.String())
	}
}

func spreadRule(name string, extra map[string]any) map[string]any {
	rule := map[string]any{
		"name": name, "enabled": true, "kind": "spread", "min_spread_bps": "15", "min_liquidity_quote": "1000",
		"min_lifetime_s": 0, "buy_venues": []string{"binance"}, "sell_venues": []string{"okx"}, "quotes": []string{"USDT"},
		"bases_allow": []string{}, "bases_deny": []string{}, "cooldown_s": 600, "telegram": false, "auto_paper": false, "paper_size_quote": "100",
	}
	for k, v := range extra {
		rule[k] = v
	}
	return rule
}

// TestEntitlementEnforcement covers packages.md §3.2 through the API:
// rule count, kind, venues, cooldown, Telegram channel, auto-paper
// strategy and size cap, template count, refresh floor, seats.
func TestEntitlementEnforcement(t *testing.T) {
	_, mux, _, _ := newTenantServer(t)
	cookie, csrf := login(t, mux, "owner@b.test", "b-owner-pw") // Watch: 2 rules, spread+basis, fixed venues, 300 s cooldown
	expect := func(rec *httptest.ResponseRecorder, key string) {
		t.Helper()
		var env struct {
			Data  map[string]any `json:"data"`
			Error *APIError      `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if rec.Code != http.StatusForbidden || env.Error == nil || env.Error.Code != "entitlement_exceeded" || env.Data["key"] != key {
			t.Fatalf("want entitlement_exceeded %s, got %d %s", key, rec.Code, rec.Body.String())
		}
		if env.Error.Message == "" || strings.Contains(env.Error.Message, "%") {
			t.Fatalf("message not clear: %q", env.Error.Message)
		}
	}
	post := func(body any) *httptest.ResponseRecorder {
		return call(t, mux, http.MethodPost, "/api/v1/screener/rules", cookie, csrf, body)
	}
	expect(post(spreadRule("carry", map[string]any{"kind": "carry", "min_carry_apr": "5", "min_spread_bps": nil})), "rules.kinds")
	expect(post(spreadRule("gate", map[string]any{"sell_venues": []string{"gate"}})), "venues.screener_fixed")
	expect(post(spreadRule("cool", map[string]any{"cooldown_s": 30})), "alerts.min_cooldown_s")
	expect(post(spreadRule("tg", map[string]any{"telegram": true})), "alerts.channels")
	expect(post(spreadRule("ap", map[string]any{"auto_paper": true})), "auto_paper.strategies")
	if rec := post(spreadRule("ok-1", nil)); rec.Code != http.StatusCreated {
		t.Fatalf("rule 1 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(spreadRule("ok-2", nil)); rec.Code != http.StatusCreated {
		t.Fatalf("rule 2 = %d %s", rec.Code, rec.Body.String())
	}
	expect(post(spreadRule("ok-3", nil)), "rules.max_active")

	// Signal: auto-paper cross-venue spot allowed up to 5000 quote; 6 venues.
	acookie, acsrf := login(t, mux, "owner@a.test", "a-owner-pw")
	apost := func(body any) *httptest.ResponseRecorder {
		return call(t, mux, http.MethodPost, "/api/v1/screener/rules", acookie, acsrf, body)
	}
	expect(apost(spreadRule("big", map[string]any{"auto_paper": true, "paper_size_quote": "5000.01", "cooldown_s": 60})), "auto_paper.max_size_quote")
	if rec := apost(spreadRule("fits", map[string]any{"auto_paper": true, "paper_size_quote": "5000.00", "cooldown_s": 60, "sell_venues": []string{"gate"}})); rec.Code != http.StatusCreated {
		t.Fatalf("signal auto-paper = %d %s", rec.Code, rec.Body.String())
	}
	expect(apost(spreadRule("many", map[string]any{"buy_venues": []string{"binance", "okx", "bybit", "bitget"}, "sell_venues": []string{"gate", "mexc", "kucoin"}, "cooldown_s": 60})), "venues.screener_max")

	// Templates: Watch allows 3.
	for i := 0; i < 3; i++ {
		if rec := call(t, mux, http.MethodPost, "/api/v1/screener/templates", cookie, csrf, map[string]any{"name": "t", "filters": map[string]any{}}); rec.Code != http.StatusCreated {
			t.Fatalf("template %d = %d %s", i, rec.Code, rec.Body.String())
		}
	}
	expect(call(t, mux, http.MethodPost, "/api/v1/screener/templates", cookie, csrf, map[string]any{"name": "t", "filters": map[string]any{}}), "rules.templates_max")

	// Settings: refresh floor (Watch 30 s) and venue count/fixed set.
	rec := call(t, mux, http.MethodGet, "/api/v1/screener/settings", cookie, "", nil)
	data, _ := envelope(t, rec)
	version := int64(data["version"].(float64))
	settings := data["settings"].(map[string]any)
	settings["poll_interval_s"] = 5
	expect(call(t, mux, http.MethodPost, "/api/v1/screener/settings", cookie, csrf, map[string]any{"settings": settings, "parent_version": version}), "rules.min_refresh_s")

	// Seats: Watch has 1 seat, owner only.
	expect(call(t, mux, http.MethodPost, "/api/v1/org/members", cookie, csrf, map[string]any{"user_id": "nobody", "role": "VIEWER"}), "seats.max")
	// Signal: 1 seat too, but role check comes after the seat count;
	// a viewer may not manage members at all.
	vcookie, vcsrf := login(t, mux, "viewer@a.test", "a-viewer-pw")
	if rec := call(t, mux, http.MethodPost, "/api/v1/org/members", vcookie, vcsrf, map[string]any{"user_id": "nobody", "role": "VIEWER"}); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer adds member = %d", rec.Code)
	}
	if rec := call(t, mux, http.MethodGet, "/api/v1/org/members", vcookie, "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"a-viewer"`) {
		t.Fatalf("viewer lists members = %d %s", rec.Code, rec.Body.String())
	}
}

// TestOrgScopingThroughAPI: a user of organisation B cannot read,
// update or delete organisation A's rules; each organisation sees only
// its own list.
func TestOrgScopingThroughAPI(t *testing.T) {
	_, mux, _, src := newTenantServer(t)
	src.Set(entitlements.Input{OrgID: 3, PackageCode: "signal", SubStatus: "active"})
	acookie, acsrf := login(t, mux, "owner@a.test", "a-owner-pw")
	bcookie, bcsrf := login(t, mux, "owner@b.test", "b-owner-pw")
	rec := call(t, mux, http.MethodPost, "/api/v1/screener/rules", acookie, acsrf, spreadRule("A's", map[string]any{"cooldown_s": 60}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("A create = %d %s", rec.Code, rec.Body.String())
	}
	data, _ := envelope(t, rec)
	ruleID := data["rule"].(map[string]any)["id"].(string)
	if rec := call(t, mux, http.MethodPost, "/api/v1/screener/rules", bcookie, bcsrf, spreadRule("B's", map[string]any{"cooldown_s": 60})); rec.Code != http.StatusCreated {
		t.Fatalf("B create = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, mux, http.MethodGet, "/api/v1/screener/rules", bcookie, "", nil)
	if strings.Contains(rec.Body.String(), ruleID) || !strings.Contains(rec.Body.String(), `"B's"`) {
		t.Fatalf("B list = %s", rec.Body.String())
	}
	rec = call(t, mux, http.MethodGet, "/api/v1/screener/rules", acookie, "", nil)
	if !strings.Contains(rec.Body.String(), ruleID) || strings.Contains(rec.Body.String(), `"B's"`) {
		t.Fatalf("A list = %s", rec.Body.String())
	}
	if rec := call(t, mux, http.MethodPut, "/api/v1/screener/rules/"+ruleID, bcookie, bcsrf, spreadRule("hijack", map[string]any{"cooldown_s": 60})); rec.Code != http.StatusNotFound {
		t.Fatalf("B update A = %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, mux, http.MethodDelete, "/api/v1/screener/rules/"+ruleID, bcookie, bcsrf, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("B delete A = %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, mux, http.MethodDelete, "/api/v1/screener/rules/"+ruleID, acookie, acsrf, nil); rec.Code != http.StatusOK {
		t.Fatalf("A delete own = %d %s", rec.Code, rec.Body.String())
	}
}

// TestOverrideRouteRejectsLive: the platform operator's override route
// validates the merged document (compliance #23).
func TestOverrideRouteRejectsLive(t *testing.T) {
	_, mux, ts, _ := newTenantServer(t)
	cookie, csrf := login(t, mux, "admin@example.test", "admin-pw")
	if rec := call(t, mux, http.MethodPut, "/api/v1/orgs/2/override", cookie, csrf, `{"execution":{"live":true}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("live override = %d %s", rec.Code, rec.Body.String())
	}
	if org, _ := ts.Org(context.Background(), 2); len(org.EntitlementsOverride) != 0 {
		t.Fatal("rejected override was stored")
	}
	if rec := call(t, mux, http.MethodPut, "/api/v1/orgs/2/override", cookie, csrf, `{"rules":{"max_active":12}}`); rec.Code != http.StatusOK {
		t.Fatalf("valid override = %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, mux, http.MethodPost, "/api/v1/orgs", cookie, csrf, `{"name":"New Tenant","owner_user_id":"nobody","country":"cy","customer_type":"consumer"}`); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"package_code":"operator"`) || !strings.Contains(rec.Body.String(), `"trial_ends_at":"`) {
		t.Fatalf("create org = %d %s", rec.Code, rec.Body.String())
	}
}

// TestPlatformSettingsDocumentIsOperatorOnly (S5): the settings
// document — mode, venues, fees, paper balances, telegram allowlist —
// answers only to platform_admin. A tenant ADMIN (console role) and
// platform staff without the flag are refused on the read, the version
// history and the preview dry-run.
func TestPlatformSettingsDocumentIsOperatorOnly(t *testing.T) {
	s, mux, _, _ := newTenantServer(t)
	svc := platform.NewService(platform.NewMemoryStore(), discardLogger(), nil)
	if _, err := svc.Load(context.Background(), config.Bootstrap{
		Mode: config.ModePaper, Symbols: []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
		StartingAssets: []string{"USDT"}, PaperBalance: "10000",
	}); err != nil {
		t.Fatal(err)
	}
	s.Platform = svc

	for _, u := range []struct{ email, pw string }{{"owner@a.test", "a-owner-pw"}, {"staff@example.test", "staff-pw"}} {
		cookie, _ := login(t, mux, u.email, u.pw)
		for _, path := range []string{"/api/v1/platform/settings", "/api/v1/platform/settings/versions"} {
			if rec := call(t, mux, http.MethodGet, path, cookie, "", nil); rec.Code != http.StatusForbidden {
				t.Fatalf("%s GET %s = %d", u.email, path, rec.Code)
			}
		}
		if rec := call(t, mux, http.MethodPost, "/api/v1/platform/settings/preview", cookie, "csrf", `{}`); rec.Code != http.StatusForbidden {
			t.Fatalf("%s preview = %d", u.email, rec.Code)
		}
	}
	cookie, _ := login(t, mux, "admin@example.test", "admin-pw")
	if rec := call(t, mux, http.MethodGet, "/api/v1/platform/settings", cookie, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("platform admin GET = %d %s", rec.Code, rec.Body.String())
	}
}

// TestUserRoleChangeMovesPlatformAdmin (S8): platform_admin follows the
// console role on every change (CreateUser's insert-time rule), so a
// promotion cannot leave a "quiet admin" and a demotion cannot leave a
// VIEWER holding operator surfaces.
func TestUserRoleChangeMovesPlatformAdmin(t *testing.T) {
	s, mux, _, _ := newTenantServer(t)
	adminCookie, adminCSRF := login(t, mux, "admin@example.test", "admin-pw")

	rec := call(t, mux, http.MethodPost, "/api/v1/users/nobody/role", adminCookie, adminCSRF, `{"role":"ADMIN"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote = %d %s", rec.Code, rec.Body.String())
	}
	u, err := s.Users.UserByID(context.Background(), "nobody")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != auth.RoleAdmin || !u.PlatformAdmin {
		t.Fatalf("after promotion = %+v", u)
	}

	rec = call(t, mux, http.MethodPost, "/api/v1/users/nobody/role", adminCookie, adminCSRF, `{"role":"VIEWER"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("demote = %d %s", rec.Code, rec.Body.String())
	}
	u, err = s.Users.UserByID(context.Background(), "nobody")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != auth.RoleViewer || u.PlatformAdmin {
		t.Fatalf("after demotion = %+v", u)
	}
}

// TestOrgMemberAddRequiresConsentByFirstSeat (S10): adding an account
// that already belongs to any organisation — this one included — is
// refused until the invitation flow (T-085) exists; a first-time
// account can still be seated by its org managers.
func TestOrgMemberAddRequiresConsentByFirstSeat(t *testing.T) {
	s, mux, ts, src := newTenantServer(t)
	// Org B on the operator package (3 seats, owner/admin/viewer) so the
	// entitlement gate is not what the refusal cases trip on.
	src.Set(entitlements.Input{OrgID: 3, PackageCode: entitlements.PackageOperator, SubStatus: "active"})
	s.Entitlements.Invalidate(3)
	cookie, csrf := login(t, mux, "owner@b.test", "b-owner-pw")

	// Already a member of THIS org → duplicate.
	if rec := call(t, mux, http.MethodPost, "/api/v1/org/members", cookie, csrf, `{"user_id":"b-owner","role":"VIEWER"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "duplicate_member") {
		t.Fatalf("duplicate = %d %s", rec.Code, rec.Body.String())
	}
	// Member of ANOTHER org → consent refusal (the added account never
	// agreed to appear in this roster).
	if rec := call(t, mux, http.MethodPost, "/api/v1/org/members", cookie, csrf, `{"user_id":"a-viewer","role":"VIEWER"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "user_already_member") {
		t.Fatalf("cross-org add = %d %s", rec.Code, rec.Body.String())
	}
	// A first-time account (no membership anywhere) can be seated.
	if rec := call(t, mux, http.MethodPost, "/api/v1/org/members", cookie, csrf, `{"user_id":"nobody","role":"VIEWER"}`); rec.Code != http.StatusCreated {
		t.Fatalf("first-seat add = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := ts.MembershipFor(context.Background(), 3, "nobody"); err != nil {
		t.Fatalf("membership missing after add: %v", err)
	}
}

// TestOrgRosterEmailsAreManagerOnly (S10): member e-mail addresses are
// the managers' information; a VIEWER seat sees the roster without
// them, the org's owner with them.
func TestOrgRosterEmailsAreManagerOnly(t *testing.T) {
	// The pgx store joins e-mails into the roster (the memory harness
	// does not), so the disclosure rule is proven at the layer that
	// applies it: rosterView with the three principal shapes that
	// matter, plus the HTTP path staying sound for a non-manager.
	s, mux, _, _ := newTenantServer(t)
	roster := []tenancy.Membership{
		{OrgID: 2, UserID: "a-owner", Role: tenancy.RoleOwner, Email: "owner@a.test"},
		{OrgID: 2, UserID: "a-viewer", Role: tenancy.RoleViewer, Email: "viewer@a.test"},
	}

	for _, p := range []Principal{
		{UserID: "u-admin", Role: auth.RoleAdmin, PlatformAdmin: true, OrgID: 1},
		{UserID: "a-owner", OrgID: 2, OrgRole: tenancy.RoleOwner},
		{UserID: "a-owner", OrgID: 2, OrgRole: tenancy.RoleAdmin},
	} {
		out := s.rosterView(p, roster)
		if len(out) != 2 || out[0].Email != "owner@a.test" || out[1].Email != "viewer@a.test" {
			t.Fatalf("manager/platform view must keep e-mails: %+v (principal %+v)", out, p)
		}
		// The caller's slice is never mutated in place.
		if roster[0].Email != "owner@a.test" {
			t.Fatal("rosterView mutated the input roster")
		}
	}
	for _, p := range []Principal{
		{UserID: "a-viewer", OrgID: 2, OrgRole: tenancy.RoleViewer},
		{UserID: "a-member", OrgID: 2, OrgRole: tenancy.RoleMember},
	} {
		out := s.rosterView(p, roster)
		if len(out) != 2 || out[0].Email != "" || out[1].Email != "" {
			t.Fatalf("non-manager view must strip e-mails: %+v (principal %+v)", out, p)
		}
	}

	// The route itself stays sound for a non-manager (the memory store
	// carries no e-mails; the strip is the no-op path there).
	viewerCookie, _ := login(t, mux, "viewer@a.test", "a-viewer-pw")
	if rec := call(t, mux, http.MethodGet, "/api/v1/org/members", viewerCookie, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("viewer roster = %d %s", rec.Code, rec.Body.String())
	}
}

// TestOrgAPIKeyListingRequiresManager (S10): the key inventory (prefixes
// and usage of every seat's credentials) is manager information.
func TestOrgAPIKeyListingRequiresManager(t *testing.T) {
	_, mux, _, _ := newTenantServer(t)
	viewerCookie, _ := login(t, mux, "viewer@a.test", "a-viewer-pw")
	if rec := call(t, mux, http.MethodGet, "/api/v1/org/api-keys", viewerCookie, "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer key list = %d", rec.Code)
	}
	ownerCookie, _ := login(t, mux, "owner@a.test", "a-owner-pw")
	if rec := call(t, mux, http.MethodGet, "/api/v1/org/api-keys", ownerCookie, "", nil); rec.Code == http.StatusForbidden {
		t.Fatalf("manager key list = %d %s", rec.Code, rec.Body.String())
	}
}
