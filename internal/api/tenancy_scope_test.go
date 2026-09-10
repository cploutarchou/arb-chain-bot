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
	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/report"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// trimVenues keeps only two tier-1 venues enabled so a Signal
// organisation's venues.screener_max / venues.screener_tiers checks pass:
// the defaults enable every venue the screener knows.
func trimVenues(settings map[string]any) {
	venues, _ := settings["venues"].(map[string]any)
	for id, raw := range venues {
		if v, ok := raw.(map[string]any); ok {
			v["enabled"] = id == "binance" || id == "okx"
		}
	}
}

// callOrg is call() with an explicit X-Org-ID header.
func callOrg(t *testing.T, mux *http.ServeMux, method, path string, cookie *http.Cookie, csrf, org string, body any) *httptest.ResponseRecorder {
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
	if org != "" {
		req.Header.Set(orgHeader, org)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestDualMembershipResolvesToOrganisation (security audit S4): an
// account that belongs to the platform organisation (joined first, the
// way CreateUser joins every console account) AND to a tenant
// organisation acts in the tenant organisation; X-Org-ID selects among
// its memberships, an organisation it does not belong to is refused,
// and a write in the platform scope takes platform_admin even though
// the account holds an ADMIN membership there and the console ADMIN
// role.
func TestDualMembershipResolvesToOrganisation(t *testing.T) {
	s, mux, ts, _ := newTenantServer(t)
	users, ok := s.Auth.Users.(*auth.MemoryStore)
	if !ok {
		t.Fatal("newTenantServer's auth store is not *auth.MemoryStore")
	}
	hash, err := auth.HashPassword("dual-pw")
	if err != nil {
		t.Fatal(err)
	}
	users.AddUser(auth.User{ID: "dual", Email: "dual@example.test", PasswordHash: hash, Role: auth.RoleAdmin, CreatedAt: time.Now()})
	ctx := context.Background()
	if err := ts.AddMember(ctx, tenancy.Membership{OrgID: 1, UserID: "dual", Role: tenancy.RoleAdmin, CreatedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := ts.AddMember(ctx, tenancy.Membership{OrgID: 2, UserID: "dual", Role: tenancy.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := login(t, mux, "dual@example.test", "dual-pw")

	rec := call(t, mux, http.MethodGet, "/api/v1/me", cookie, "", nil)
	data, _ := envelope(t, rec)
	if rec.Code != http.StatusOK || data["org"].(map[string]any)["id"] != float64(2) || data["org_role"] != "ADMIN" {
		t.Fatalf("default me = %d %s", rec.Code, rec.Body.String())
	}
	ms, _ := data["memberships"].([]any)
	if len(ms) != 2 || ms[0].(map[string]any)["org_id"] != float64(2) || ms[1].(map[string]any)["org_id"] != float64(1) {
		t.Fatalf("memberships = %v", data["memberships"])
	}
	// Explicit platform scope.
	rec = callOrg(t, mux, http.MethodGet, "/api/v1/me", cookie, "", "1", nil)
	data, _ = envelope(t, rec)
	if rec.Code != http.StatusOK || data["org"].(map[string]any)["id"] != float64(1) || data["platform_admin"] != false {
		t.Fatalf("platform me = %d %s", rec.Code, rec.Body.String())
	}
	// An organisation the account is not a member of; a malformed header.
	rec = callOrg(t, mux, http.MethodGet, "/api/v1/me", cookie, "", "3", nil)
	if _, e := envelope(t, rec); rec.Code != http.StatusForbidden || e == nil || e.Code != "org_forbidden" {
		t.Fatalf("foreign org = %d %s", rec.Code, rec.Body.String())
	}
	if rec := callOrg(t, mux, http.MethodGet, "/api/v1/me", cookie, "", "one", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad header = %d %s", rec.Code, rec.Body.String())
	}

	// Writes: the tenant organisation accepts them; the platform scope
	// refuses them without platform_admin, reads there stay open.
	rule := spreadRule("dual's", map[string]any{"cooldown_s": 60})
	if rec := call(t, mux, http.MethodPost, "/api/v1/screener/rules", cookie, csrf, rule); rec.Code != http.StatusCreated {
		t.Fatalf("tenant write = %d %s", rec.Code, rec.Body.String())
	}
	rec = callOrg(t, mux, http.MethodPost, "/api/v1/screener/rules", cookie, csrf, "1", rule)
	if _, e := envelope(t, rec); rec.Code != http.StatusForbidden || e == nil || e.Code != "platform_admin_required" {
		t.Fatalf("platform write = %d %s", rec.Code, rec.Body.String())
	}
	if rec := callOrg(t, mux, http.MethodPost, "/api/v1/org/members", cookie, csrf, "1", map[string]any{"user_id": "nobody", "role": "VIEWER"}); rec.Code != http.StatusForbidden {
		t.Fatalf("platform member add = %d %s", rec.Code, rec.Body.String())
	}
	if rec := callOrg(t, mux, http.MethodGet, "/api/v1/screener/rules", cookie, "", "1", nil); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "dual's") {
		t.Fatalf("platform read = %d %s", rec.Code, rec.Body.String())
	}
	// The platform admin passes the gate.
	acookie, acsrf := login(t, mux, "admin@example.test", "admin-pw")
	if rec := callOrg(t, mux, http.MethodPost, "/api/v1/screener/rules", acookie, acsrf, "1", spreadRule("platform's", map[string]any{"cooldown_s": 60})); rec.Code != http.StatusCreated {
		t.Fatalf("platform admin write = %d %s", rec.Code, rec.Body.String())
	}
}

// TestPlatformScopeWritesRequirePlatformAdmin: an org-1 ADMIN
// membership without platform_admin (u-staff) reads the platform
// screener but may not change its settings, rules, reports or roster;
// the platform admin still can.
func TestPlatformScopeWritesRequirePlatformAdmin(t *testing.T) {
	s, mux, _, _ := newTenantServer(t)
	s.ScreenerReports = &report.Generator{Svc: s.Screener, Ledger: paperexec.NewMemoryLedger(), Store: report.NewMemoryStore(), Log: discardLogger()}
	cookie, csrf := login(t, mux, "staff@example.test", "staff-pw")
	rec := call(t, mux, http.MethodGet, "/api/v1/screener/settings", cookie, "", nil)
	data, _ := envelope(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("staff read = %d %s", rec.Code, rec.Body.String())
	}
	for _, rt := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/v1/screener/settings", map[string]any{"settings": data["settings"], "parent_version": data["version"]}},
		{http.MethodPost, "/api/v1/screener/rules", spreadRule("staff's", map[string]any{"cooldown_s": 60})},
		{http.MethodPost, "/api/v1/screener/reports/run", `{}`},
		{http.MethodPost, "/api/v1/org/members", map[string]any{"user_id": "nobody", "role": "VIEWER"}},
	} {
		rec := call(t, mux, rt.method, rt.path, cookie, csrf, rt.body)
		if _, e := envelope(t, rec); rec.Code != http.StatusForbidden || e == nil || e.Code != "platform_admin_required" {
			t.Fatalf("%s %s = %d %s", rt.method, rt.path, rec.Code, rec.Body.String())
		}
	}
	acookie, acsrf := login(t, mux, "admin@example.test", "admin-pw")
	if rec := call(t, mux, http.MethodPost, "/api/v1/screener/reports/run", acookie, acsrf, `{}`); rec.Code != http.StatusOK {
		t.Fatalf("platform admin run = %d %s", rec.Code, rec.Body.String())
	}
}

// TestScreenerSettingsAndReportsAreOrgScoped (security S7, database
// D1/D2): organisation A's settings change is invisible to B and to the
// process-wide (engine) document, B cannot build on A's version, and
// A's on-demand reports are neither listed nor readable by B, whose
// last_run stays its own.
func TestScreenerSettingsAndReportsAreOrgScoped(t *testing.T) {
	s, mux, ts, src := newTenantServer(t)
	src.Set(entitlements.Input{OrgID: 3, PackageCode: "signal", SubStatus: "active"})
	ledger := paperexec.NewMemoryLedger()
	s.ScreenerReports = &report.Generator{Svc: s.Screener, Ledger: ledger, Store: report.NewMemoryStore(), Orgs: ts, Log: discardLogger(), Dir: t.TempDir()}
	exec := paperexec.Execution{ID: "e1", RuleID: "r1", Strategy: screener.StrategyCrossVenueSpot, Kind: paperexec.KindSpot,
		Base: "BTC", Quote: "USDT", VenueA: screener.VenueBinance, VenueB: screener.VenueOKX, At: time.Now().UTC().Add(-2 * time.Hour),
		PnLQuote: decimalFrom("1.5"), Fills: []paperexec.Fill{{Leg: 1, Venue: screener.VenueBinance, Side: "BUY", Qty: decimalFrom("0.1"), FillPrice: decimalFrom("50000"), Status: "FILLED"}}}
	if err := ledger.InsertExecution(context.Background(), exec); err != nil {
		t.Fatal(err)
	}
	acookie, acsrf := login(t, mux, "owner@a.test", "a-owner-pw")
	bcookie, bcsrf := login(t, mux, "owner@b.test", "b-owner-pw")

	// Settings: A's write produces A's new active version only.
	rec := call(t, mux, http.MethodGet, "/api/v1/screener/settings", acookie, "", nil)
	data, _ := envelope(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("A settings = %d %s", rec.Code, rec.Body.String())
	}
	aVersion := int64(data["version"].(float64))
	settings := data["settings"].(map[string]any)
	settings["poll_interval_s"] = 30
	settings["min_liquidity_quote"] = "4242"
	trimVenues(settings)
	rec = call(t, mux, http.MethodPost, "/api/v1/screener/settings", acookie, acsrf, map[string]any{"settings": settings, "parent_version": aVersion})
	data, _ = envelope(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("A apply = %d %s", rec.Code, rec.Body.String())
	}
	aNew := int64(data["version"].(float64))
	if aNew <= aVersion {
		t.Fatalf("A version %d -> %d", aVersion, aNew)
	}
	rec = call(t, mux, http.MethodGet, "/api/v1/screener/settings", bcookie, "", nil)
	data, _ = envelope(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("B settings = %d %s", rec.Code, rec.Body.String())
	}
	bSettings := data["settings"].(map[string]any)
	if int64(data["version"].(float64)) == aNew || bSettings["min_liquidity_quote"] == "4242" {
		t.Fatalf("B sees A's settings: %s", rec.Body.String())
	}
	if s.Screener.Current().Settings.MinLiquidityQuote.Equal(decimalFrom("4242")) {
		t.Fatal("a tenant write reached the process-wide document")
	}
	// B cannot build on A's version: its own active version differs.
	bSettings["poll_interval_s"] = 30
	trimVenues(bSettings)
	rec = call(t, mux, http.MethodPost, "/api/v1/screener/settings", bcookie, bcsrf, map[string]any{"settings": bSettings, "parent_version": aNew})
	if _, e := envelope(t, rec); rec.Code != http.StatusConflict || e == nil || e.Code != "stale_version" {
		t.Fatalf("B on A's version = %d %s", rec.Code, rec.Body.String())
	}
	// A's views price with A's document.
	rec = call(t, mux, http.MethodGet, "/api/v1/screener/status", acookie, "", nil)
	if data, _ := envelope(t, rec); rec.Code != http.StatusOK || data["poll_interval_s"] != float64(30) {
		t.Fatalf("A status = %d %s", rec.Code, rec.Body.String())
	}

	// Reports: A's on-demand run files reports in A's scope only.
	rec = call(t, mux, http.MethodPost, "/api/v1/screener/reports/run", acookie, acsrf, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("A run = %d %s", rec.Code, rec.Body.String())
	}
	var runEnv struct {
		Data struct {
			Run report.RunResult `json:"run"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &runEnv); err != nil {
		t.Fatal(err)
	}
	if runEnv.Data.Run.OrgID != 2 || len(runEnv.Data.Run.Reports) == 0 || !strings.HasSuffix(runEnv.Data.Run.Dir, "org-2") {
		t.Fatalf("A run = %+v", runEnv.Data.Run)
	}
	reportID := runEnv.Data.Run.Reports[0].ID
	type listEnvelope struct {
		Data struct {
			Reports []report.Summary  `json:"reports"`
			LastRun *report.RunResult `json:"last_run"`
		} `json:"data"`
	}
	var aList, bList listEnvelope
	rec = call(t, mux, http.MethodGet, "/api/v1/screener/reports", acookie, "", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &aList)
	if rec.Code != http.StatusOK || len(aList.Data.Reports) != len(runEnv.Data.Run.Reports) || aList.Data.LastRun == nil || aList.Data.LastRun.OrgID != 2 {
		t.Fatalf("A list = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, mux, http.MethodGet, "/api/v1/screener/reports", bcookie, "", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &bList)
	if rec.Code != http.StatusOK || len(bList.Data.Reports) != 0 || bList.Data.LastRun != nil {
		t.Fatalf("B list = %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, mux, http.MethodGet, "/api/v1/screener/reports/"+reportID, bcookie, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("B reads A's report = %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, mux, http.MethodGet, "/api/v1/screener/reports/"+reportID, acookie, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("A reads own report = %d %s", rec.Code, rec.Body.String())
	}
	// B's own run stays B's; A's list is unchanged by it.
	if rec := call(t, mux, http.MethodPost, "/api/v1/screener/reports/run", bcookie, bcsrf, `{}`); rec.Code != http.StatusOK {
		t.Fatalf("B run = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, mux, http.MethodGet, "/api/v1/screener/reports", bcookie, "", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &bList)
	if len(bList.Data.Reports) == 0 || strings.Contains(rec.Body.String(), reportID) || bList.Data.LastRun == nil || bList.Data.LastRun.OrgID != 3 {
		t.Fatalf("B list after run = %s", rec.Body.String())
	}
	rec = call(t, mux, http.MethodGet, "/api/v1/screener/reports", acookie, "", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &aList)
	if len(aList.Data.Reports) != len(runEnv.Data.Run.Reports) || strings.Contains(rec.Body.String(), bList.Data.Reports[0].ID) {
		t.Fatalf("A list after B's run = %s", rec.Body.String())
	}
}
