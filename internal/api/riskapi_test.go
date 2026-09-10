package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

// fakeBreakers is a BreakerController over a real registry, so the
// route's contract is tested against the same state machine the engine
// runs (Trip → operator Close → CLOSED, observer firing on transition).
type fakeBreakers struct {
	reg         *risk.Registry
	transitions []risk.Transition
}

func (f *fakeBreakers) CloseBreaker(name, scope string) (risk.BreakerState, bool) {
	if _, ok := f.reg.State(name, scope); !ok {
		return risk.BreakerClosed, false
	}
	f.reg.Close(name, scope, time.Now())
	st, _ := f.reg.State(name, scope)
	return st, true
}

// Denial matrix + happy path for POST /api/v1/risk/breakers/close:
// requirePerm (risk:config, ADMIN only) → requireCSRF → 404 (absent) →
// 400 (confirmation) → 404 (unknown breaker) → 200 with the resulting
// state, one audit row, and the registry's own transition observer
// firing so risk_events and the notification both see the close.
func TestBreakerCloseRoute(t *testing.T) {
	s, mux := newTestServer(t)

	viewerCookie, viewerCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	opCookie, opCSRF := login(t, mux, "op@example.test", "op-pw")
	adminCookie, adminCSRF := login(t, mux, "admin@example.test", "admin-pw")

	// RBAC: only ADMIN holds risk:config.
	if rec := postJSON(t, mux, viewerCookie, viewerCSRF, "/api/v1/risk/breakers/close", `{"name":"daily_loss","confirm":"daily_loss"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer close = %d", rec.Code)
	}
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/risk/breakers/close", `{"name":"daily_loss","confirm":"daily_loss"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("operator close = %d", rec.Code)
	}
	// CSRF mandatory.
	if rec := postJSON(t, mux, adminCookie, "", "/api/v1/risk/breakers/close", `{"name":"daily_loss","confirm":"daily_loss"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("missing csrf = %d", rec.Code)
	}
	// No registry in this profile: honest 404 before any parsing.
	if rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/risk/breakers/close", `not even json`); rec.Code != http.StatusNotFound {
		t.Fatalf("absent breakers = %d %s", rec.Code, rec.Body.String())
	}

	var fb *fakeBreakers
	fb = &fakeBreakers{reg: risk.NewRegistry(func(tr risk.Transition) { fb.transitions = append(fb.transitions, tr) })}
	fb.reg.Register("daily_loss", "", 0)
	fb.reg.Trip("daily_loss", "", "USDT session loss 500 reached the limit 500", time.Now())
	s.Breakers = fb
	var audits []struct{ action, entity string }
	s.AuditAction = func(_, action, entity, _, _ string, _ []byte) {
		audits = append(audits, struct{ action, entity string }{action, entity})
	}

	// Type-to-confirm: the confirmation must repeat the breaker's own
	// name, so a stray POST can never silence an open breaker.
	if rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/risk/breakers/close", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no confirm = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/risk/breakers/close", `{"name":"daily_loss","confirm":"CLOSE"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("generic confirm = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/risk/breakers/close", `not even json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body = %d", rec.Code)
	}
	if rec := postJSON(t, mux, adminCookie, adminCSRF, `/api/v1/risk/breakers/close`, `{"name":"","confirm":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank name = %d", rec.Code)
	}
	if len(fb.transitions) != 1 { // only the Trip above; nothing closed yet
		t.Fatalf("transitions = %d, want 1 (the trip only)", len(fb.transitions))
	}

	// Unknown breaker (typo, or not registered in this run): honest 404.
	if rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/risk/breakers/close", `{"name":"dialy_loss","confirm":"dialy_loss"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown breaker = %d %s", rec.Code, rec.Body.String())
	}

	rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/risk/breakers/close", `{"name":"daily_loss","confirm":"daily_loss"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("close = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"state":"CLOSED"`) {
		t.Fatalf("close payload = %s", rec.Body.String())
	}
	if st, ok := fb.reg.State("daily_loss", ""); !ok || st != risk.BreakerClosed {
		t.Fatalf("registry state after close = %v/%v", st, ok)
	}
	if len(fb.transitions) != 2 || fb.transitions[1].To != risk.BreakerClosed {
		t.Fatalf("close transition missing: %+v", fb.transitions)
	}
	if len(audits) != 1 {
		t.Fatalf("close audit rows = %d, want 1: %+v", len(audits), audits)
	}
	if audits[0].action != "risk.breaker_close" || !strings.Contains(audits[0].entity, "daily_loss") {
		t.Fatalf("audit row = %+v", audits[0])
	}
}
