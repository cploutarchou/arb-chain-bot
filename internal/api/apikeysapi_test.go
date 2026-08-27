package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/apikey"
	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// newAPIKeyServer extends newTenantServer with an in-memory API-key
// store/rate limiter and two more organisations: "operator" (org 4,
// API read-only, keys_max 2) and "desk" (org 5, API read + rules:write
// + templates:write, keys_max 10) — packages.md §2's two lowest tiers
// with any client API access. Every new org's OWNER/VIEWER gets a
// password so tests can log in normally.
func newAPIKeyServer(t *testing.T) (s *Server, mux *http.ServeMux, ts *tenancy.MemoryStore, src *entitlements.MapSource) {
	t.Helper()
	s, mux, ts, src = newTenantServer(t)
	s.APIKeys = apikey.NewMemoryStore()
	s.APIRateLimiter = entitlements.NewRateLimiter()

	users, ok := s.Auth.Users.(*auth.MemoryStore)
	if !ok {
		t.Fatal("newTenantServer's auth store is not *auth.MemoryStore")
	}
	addUser := func(id, email, pw string) {
		hash, err := auth.HashPassword(pw)
		if err != nil {
			t.Fatal(err)
		}
		users.AddUser(auth.User{ID: id, Email: email, PasswordHash: hash, Role: auth.RoleAdmin, CreatedAt: time.Now()})
	}
	addUser("operator-owner", "owner@operator.test", "operator-owner-pw")
	addUser("desk-owner", "owner@desk.test", "desk-owner-pw")
	addUser("desk-viewer", "viewer@desk.test", "desk-viewer-pw")

	ctx := context.Background()
	orgOperator, err := ts.CreateOrg(ctx, tenancy.Org{Name: "Operator Co", PackageCode: "operator", RiskAckVersion: "2026-08-27"}, "operator-owner")
	if err != nil {
		t.Fatal(err)
	}
	orgDesk, err := ts.CreateOrg(ctx, tenancy.Org{Name: "Desk Co", PackageCode: "desk", RiskAckVersion: "2026-08-27"}, "desk-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.AddMember(ctx, tenancy.Membership{OrgID: orgDesk.ID, UserID: "desk-viewer", Role: tenancy.RoleViewer}); err != nil {
		t.Fatal(err)
	}
	src.Set(entitlements.Input{OrgID: orgOperator.ID, PackageCode: "operator", SubStatus: "active"})
	src.Set(entitlements.Input{OrgID: orgDesk.ID, PackageCode: "desk", SubStatus: "active"})
	return s, mux, ts, src
}

// bearerCall issues a request authenticated with Authorization: Bearer
// <token> instead of a session cookie — the counterpart of call() in
// tenancy_test.go. It never sends X-CSRF-Token, so a passing mutating
// request proves the CSRF exemption for API-key callers.
func bearerCall(t *testing.T, mux *http.ServeMux, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	req.RemoteAddr = "203.0.113.11:6666"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// createAPIKey drives the console (session) creation endpoint.
func createAPIKey(t *testing.T, mux *http.ServeMux, cookie *http.Cookie, csrf, name string, scopes []string) (token string, rec apikey.Redacted, httpRec *httptest.ResponseRecorder) {
	t.Helper()
	body := map[string]any{"name": name}
	if scopes != nil {
		body["scopes"] = scopes
	}
	httpRec = call(t, mux, http.MethodPost, "/api/v1/org/api-keys", cookie, csrf, body)
	data, _ := envelope(t, httpRec)
	if httpRec.Code != http.StatusCreated {
		return "", apikey.Redacted{}, httpRec
	}
	token, _ = data["key"].(string)
	raw, _ := json.Marshal(data["api_key"])
	_ = json.Unmarshal(raw, &rec)
	return token, rec, httpRec
}

func TestAPIKeyCreateListRevoke(t *testing.T) {
	_, mux, _, _ := newAPIKeyServer(t)
	deskCookie, deskCSRF := login(t, mux, "owner@desk.test", "desk-owner-pw")

	token, rec, httpRec := createAPIKey(t, mux, deskCookie, deskCSRF, "CI pipeline", []string{"read", "rules:write"})
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", httpRec.Code, httpRec.Body.String())
	}
	if token == "" || !strings.HasPrefix(token, apikey.TokenPrefix) {
		t.Fatalf("plaintext key missing or malformed: %q", token)
	}
	if rec.ID == "" || rec.Prefix == "" {
		t.Fatalf("redacted record missing id/prefix: %+v", rec)
	}
	// The plaintext must never appear anywhere in the redacted view, and
	// the response body must not contain a "hash" field at all (the
	// struct has none to leak).
	if strings.Contains(httpRec.Body.String(), `"hash"`) {
		t.Fatalf("response leaks a hash field: %s", httpRec.Body.String())
	}

	// List: redacted, includes the key, no plaintext anywhere.
	listRec := call(t, mux, http.MethodGet, "/api/v1/org/api-keys", deskCookie, "", nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), token) {
		t.Fatal("list response leaks the plaintext key")
	}
	data, _ := envelope(t, listRec)
	keys, _ := data["api_keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("api_keys = %+v", keys)
	}

	// A member without OWNER/ADMIN cannot manage keys.
	viewerCookie, viewerCSRF := login(t, mux, "viewer@desk.test", "desk-viewer-pw")
	if rec := call(t, mux, http.MethodPost, "/api/v1/org/api-keys", viewerCookie, viewerCSRF, map[string]any{"name": "nope"}); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer create = %d: %s", rec.Code, rec.Body.String())
	}

	// Revoke, then the key can no longer authenticate.
	revokeRec := call(t, mux, http.MethodDelete, "/api/v1/org/api-keys/"+rec.ID, deskCookie, deskCSRF, nil)
	if revokeRec.Code != http.StatusOK {
		t.Fatalf("revoke = %d: %s", revokeRec.Code, revokeRec.Body.String())
	}
	authRec := bearerCall(t, mux, http.MethodGet, "/api/v1/screener/rules", token, nil)
	if authRec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key authenticated: %d %s", authRec.Code, authRec.Body.String())
	}
}

// TestAPIKeyEntitlementGates covers 403 entitlement_exceeded at every
// gate packages.md §3.2 api.* describes: api.enabled false, a scope the
// package does not include, and api.keys_max.
func TestAPIKeyEntitlementGates(t *testing.T) {
	_, mux, _, _ := newAPIKeyServer(t)

	// Watch (org B, api.enabled=false): creation itself is refused.
	bCookie, bCSRF := login(t, mux, "owner@b.test", "b-owner-pw")
	if _, _, rec := createAPIKey(t, mux, bCookie, bCSRF, "no api", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("watch org create = %d: %s", rec.Code, rec.Body.String())
	} else if data, e := envelope(t, rec); e == nil || e.Code != "entitlement_exceeded" || data["key"] != "api.enabled" {
		t.Fatalf("watch org error = %+v %+v", data, e)
	}

	// Operator (api.scopes = ["read"] only): requesting rules:write is refused.
	opCookie, opCSRF := login(t, mux, "owner@operator.test", "operator-owner-pw")
	if _, _, rec := createAPIKey(t, mux, opCookie, opCSRF, "too much", []string{"rules:write"}); rec.Code != http.StatusForbidden {
		t.Fatalf("operator org scope grant = %d: %s", rec.Code, rec.Body.String())
	} else if _, e := envelope(t, rec); e == nil || e.Code != "entitlement_exceeded" {
		t.Fatalf("operator org error = %+v", e)
	}
	// A plain "read" key is fine for Operator.
	if _, _, rec := createAPIKey(t, mux, opCookie, opCSRF, "ok", nil); rec.Code != http.StatusCreated {
		t.Fatalf("operator org default-scope create = %d: %s", rec.Code, rec.Body.String())
	}

	// keys_max: Operator's package caps at 2; the org already holds one
	// from above, so one more succeeds and the third is refused.
	if _, _, rec := createAPIKey(t, mux, opCookie, opCSRF, "second", nil); rec.Code != http.StatusCreated {
		t.Fatalf("second key = %d: %s", rec.Code, rec.Body.String())
	}
	if _, _, rec := createAPIKey(t, mux, opCookie, opCSRF, "third", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("third key = %d: %s", rec.Code, rec.Body.String())
	} else if data, e := envelope(t, rec); e == nil || e.Code != "entitlement_exceeded" || data["key"] != "api.keys_max" {
		t.Fatalf("keys_max error = %+v %+v", data, e)
	}
}

// TestAPIKeyBearerAuthAndCSRFExemption: a Bearer caller reaches the
// same screener routes a session does, mutating routes work WITHOUT an
// X-CSRF-Token (the whole point of Bearer auth), and the session's
// normal cookie-based CSRF requirement is untouched for cookie callers.
func TestAPIKeyBearerAuthAndCSRFExemption(t *testing.T) {
	_, mux, _, _ := newAPIKeyServer(t)
	deskCookie, deskCSRF := login(t, mux, "owner@desk.test", "desk-owner-pw")
	token, _, createRec := createAPIKey(t, mux, deskCookie, deskCSRF, "ci", []string{"read", "rules:write"})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", createRec.Code, createRec.Body.String())
	}

	// Read.
	if rec := bearerCall(t, mux, http.MethodGet, "/api/v1/screener/rules", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("bearer read = %d: %s", rec.Code, rec.Body.String())
	}

	// Write, no X-CSRF-Token header at all.
	body := map[string]any{
		"name": "API rule", "enabled": true, "kind": "spread",
		"min_spread_bps": "50", "min_liquidity_quote": "1000", "min_lifetime_s": 10,
		"buy_venues": []string{"binance"}, "sell_venues": []string{"okx"},
		"cooldown_s": 60, "paper_size_quote": "0",
	}
	rec := bearerCall(t, mux, http.MethodPost, "/api/v1/screener/rules", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bearer create rule = %d: %s", rec.Code, rec.Body.String())
	}

	// No Authorization header and no cookie: unauthenticated, not a
	// CSRF error — the two failure modes must stay distinguishable.
	plain := httptest.NewRequest(http.MethodGet, "/api/v1/screener/rules", nil)
	plainRec := httptest.NewRecorder()
	mux.ServeHTTP(plainRec, plain)
	if plainRec.Code != http.StatusUnauthorized {
		t.Fatalf("no credentials = %d: %s", plainRec.Code, plainRec.Body.String())
	}

	// A session request WITHOUT a CSRF token still fails (the exemption
	// is Bearer-only, not global).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/screener/rules", strings.NewReader(`{}`))
	req.AddCookie(deskCookie)
	sessRec := httptest.NewRecorder()
	mux.ServeHTTP(sessRec, req)
	if sessRec.Code != http.StatusForbidden {
		t.Fatalf("session without csrf = %d: %s", sessRec.Code, sessRec.Body.String())
	}
}

// TestAPIKeyScopeForbidden covers the two independent "write scope only
// where api.scopes allows" layers: a key minted with only "read" is
// refused on a write route even though its organisation's package DOES
// allow rules:write (key-level restriction), and a platform_admin
// staff member's key never reaches a platform_admin-only route.
func TestAPIKeyScopeForbidden(t *testing.T) {
	_, mux, _, _ := newAPIKeyServer(t)
	deskCookie, deskCSRF := login(t, mux, "owner@desk.test", "desk-owner-pw")
	readOnlyToken, _, rec := createAPIKey(t, mux, deskCookie, deskCSRF, "read only", []string{"read"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	body := map[string]any{
		"name": "should fail", "enabled": true, "kind": "spread",
		"min_spread_bps": "50", "min_liquidity_quote": "1000", "min_lifetime_s": 10,
		"buy_venues": []string{"binance"}, "sell_venues": []string{"okx"},
		"cooldown_s": 60, "paper_size_quote": "0",
	}
	if got := bearerCall(t, mux, http.MethodPost, "/api/v1/screener/rules", readOnlyToken, body); got.Code != http.StatusForbidden {
		t.Fatalf("read-only key write = %d: %s", got.Code, got.Body.String())
	}

	// Platform-admin staff (u-admin, platform_admin=true) minting a key
	// on the platform organisation must still never reach a
	// platform_admin-only route via that key.
	adminCookie, adminCSRF := login(t, mux, "admin@example.test", "admin-pw")
	adminToken, _, adminRec := createAPIKey(t, mux, adminCookie, adminCSRF, "staff key", []string{"read"})
	if adminRec.Code != http.StatusCreated {
		t.Fatalf("platform admin create key = %d: %s", adminRec.Code, adminRec.Body.String())
	}
	if got := bearerCall(t, mux, http.MethodGet, "/api/v1/secrets", adminToken, nil); got.Code != http.StatusForbidden {
		t.Fatalf("api key reached platform_admin route: %d %s", got.Code, got.Body.String())
	}
}

// TestAPIKeyRateLimit429 exhausts the operator package's burst (20) on
// a single key and asserts 429 with an integer Retry-After header.
func TestAPIKeyRateLimit429(t *testing.T) {
	_, mux, _, _ := newAPIKeyServer(t)
	opCookie, opCSRF := login(t, mux, "owner@operator.test", "operator-owner-pw")
	token, _, rec := createAPIKey(t, mux, opCookie, opCSRF, "burst test", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	// Operator: rate_per_min=60, burst=20 (internal/entitlements/packages.go).
	var last *httptest.ResponseRecorder
	ok := 0
	for i := 0; i < 25; i++ {
		last = bearerCall(t, mux, http.MethodGet, "/api/v1/screener/rules", token, nil)
		if last.Code == http.StatusOK {
			ok++
			continue
		}
		break
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("after %d ok requests, got %d: %s", ok, last.Code, last.Body.String())
	}
	if ok != 20 {
		t.Fatalf("burst allowed %d requests, want 20", ok)
	}
	retryAfter := last.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("429 missing Retry-After")
	}
	if _, err := time.ParseDuration(retryAfter + "s"); err != nil {
		t.Fatalf("Retry-After = %q not an integer number of seconds: %v", retryAfter, err)
	}
	if _, e := envelope(t, last); e == nil || e.Code != "rate_limited" {
		t.Fatalf("429 body error = %+v", e)
	}
}
