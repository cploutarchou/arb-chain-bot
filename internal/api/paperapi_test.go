package api

import (
	"net/http"
	"testing"
)

// Denial matrix + happy path for POST /api/v1/paper/reset (BL-10):
// requirePerm → requireCSRF → 404 (absent) → 400 (confirmation) → 409
// (not idle) → 200.
func TestPaperResetRoute(t *testing.T) {
	s, mux := newTestServer(t)

	viewerCookie, viewerCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	opCookie, opCSRF := login(t, mux, "op@example.test", "op-pw")
	adminCookie, adminCSRF := login(t, mux, "admin@example.test", "admin-pw")

	// RBAC: only ADMIN holds paper:reset.
	if rec := postJSON(t, mux, viewerCookie, viewerCSRF, "/api/v1/paper/reset", `{"confirm":"RESET"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer reset = %d", rec.Code)
	}
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/paper/reset", `{"confirm":"RESET"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("operator reset = %d", rec.Code)
	}
	// CSRF mandatory.
	if rec := postJSON(t, mux, adminCookie, "", "/api/v1/paper/reset", `{"confirm":"RESET"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("missing csrf = %d", rec.Code)
	}
	// Absent outside PAPER mode: honest 404, checked before body parsing.
	if rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/paper/reset", `not even json`); rec.Code != http.StatusNotFound {
		t.Fatalf("absent engine = %d %s", rec.Code, rec.Body.String())
	}

	fp := &fakePaper{running: false}
	s.Paper = fp

	// Missing/blank confirmation.
	if rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/paper/reset", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no confirm = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/paper/reset", `{"confirm":"reset"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong-case confirm = %d", rec.Code)
	}
	if rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/paper/reset", `not even json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body = %d", rec.Code)
	}
	if fp.resets != 0 {
		t.Fatalf("reset invoked despite rejected confirmation: %d", fp.resets)
	}

	// Engine running / active: 409, surfaced without ever calling Reset
	// destructively (the fake still increments only on success).
	fp.resetErr = ErrPaperNotIdle
	if rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/paper/reset", `{"confirm":"RESET"}`); rec.Code != http.StatusConflict {
		t.Fatalf("not idle = %d %s", rec.Code, rec.Body.String())
	}
	fp.resetErr = nil

	rec := postJSON(t, mux, adminCookie, adminCSRF, "/api/v1/paper/reset", `{"confirm":"RESET"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset = %d %s", rec.Code, rec.Body.String())
	}
	if fp.resets != 1 {
		t.Fatalf("resets = %d, want 1", fp.resets)
	}
	if d := decodeData(t, rec); d["running"] != false {
		t.Fatalf("reset payload = %v", d)
	}
}
