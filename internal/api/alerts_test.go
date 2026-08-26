package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
)

func newAlertServer(t *testing.T) (*Server, *http.ServeMux, *notification.Center, *[]string) {
	t.Helper()
	s, mux := newTestServer(t)
	n := 0
	center := &notification.Center{
		Log:   discardLogger(),
		IDGen: func() string { n++; return "al-1" },
		Now:   func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
	center.Deliver(notification.Delivery{Event: notification.Event{
		Severity: notification.SeverityCritical, Key: "breaker:exchange:binance",
		Title: "Breaker OPEN", Body: "gap storm", At: time.Unix(1_700_000_000, 0),
	}})
	s.Alerts = center
	var audits []string
	s.AuditAction = func(actor, action, entity string) {
		audits = append(audits, actor+"|"+action+"|"+entity)
	}
	return s, mux, center, &audits
}

func TestAlertsListAndAck(t *testing.T) {
	_, mux, center, audits := newAlertServer(t)

	// Unauthenticated list refused.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth list = %d", rec.Code)
	}

	// Viewer can list.
	vCookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts?state=active", nil)
	req.AddCookie(vCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer list = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Alerts []notification.Alert `json:"alerts"`
			Active int                  `json:"active"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Alerts) != 1 || env.Data.Active != 1 {
		t.Fatalf("list = %+v", env.Data)
	}
	id := env.Data.Alerts[0].ID

	// Viewer may NOT ack (no PermAlertAck).
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alerts/"+id+"/ack", nil)
	req.AddCookie(vCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer ack = %d", rec.Code)
	}

	// Operator acks with CSRF; center state changes; audit recorded.
	oCookie, oCSRF := login(t, mux, "op@example.test", "op-pw")
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alerts/"+id+"/ack", nil)
	req.AddCookie(oCookie)
	req.Header.Set("X-CSRF-Token", oCSRF)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator ack = %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := center.Get(id); got.State != notification.AlertAcked || got.AckedBy != "u-operator" {
		t.Fatalf("center after ack = %+v", got)
	}
	if len(*audits) != 1 || (*audits)[0] != "u-operator|alert.ack|alert:"+id {
		t.Fatalf("audits = %v", *audits)
	}

	// Double ack → 409; resolve works from acked; unknown id → 404.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alerts/"+id+"/ack", nil)
	req.AddCookie(oCookie)
	req.Header.Set("X-CSRF-Token", oCSRF)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("double ack = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alerts/"+id+"/resolve", nil)
	req.AddCookie(oCookie)
	req.Header.Set("X-CSRF-Token", oCSRF)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve = %d: %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alerts/nope/ack", nil)
	req.AddCookie(oCookie)
	req.Header.Set("X-CSRF-Token", oCSRF)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing ack = %d", rec.Code)
	}
}

func TestAlertsAbsentCenter(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("absent center = %d", rec.Code)
	}
}
