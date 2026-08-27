package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/jobrun"
	"github.com/cploutarchou/arb-chain-bot/internal/replay"
)

type fakeReplays struct {
	runs       []replay.Run
	busy       bool
	notStarted bool // review P3(h): simulates Start called before Run pins the lifetime ctx
}

func (f *fakeReplays) Start(req replay.Request, actor string) (replay.Run, error) {
	if f.busy {
		return replay.Run{}, replay.ErrBusy
	}
	if f.notStarted {
		return replay.Run{}, jobrun.ErrNotStarted
	}
	req, err := req.Normalize()
	if err != nil {
		return replay.Run{}, err
	}
	run := replay.Run{ID: "RP1", Recording: req.Recording, Request: req, Status: replay.StatusQueued, Actor: actor, Total: 1}
	f.runs = append(f.runs, run)
	return run, nil
}

func (f *fakeReplays) List(context.Context, int) ([]replay.Run, error) { return f.runs, nil }

func (f *fakeReplays) Get(_ context.Context, id string) (replay.Run, error) {
	for _, r := range f.runs {
		if r.ID == id {
			r.Evaluations = 42
			return r, nil
		}
	}
	return replay.Run{}, replay.ErrNotFound
}

// TestReplayRoutes covers BL-17: permission gate on POST (PermCampaignRun
// + CSRF), view:system on the reads, an absent-runner 404, request
// validation, the busy conflict, and the id-precedence between
// GET /api/v1/replays and GET /api/v1/replays/{id}.
func TestReplayRoutes(t *testing.T) {
	s, mux := newTestServer(t)
	opCookie, opCSRF := login(t, mux, "op@example.test", "op-pw")
	if rec := getWith(t, mux, opCookie, "/api/v1/replays"); rec.Code != http.StatusNotFound {
		t.Fatalf("absent runner: %d", rec.Code)
	}
	fr := &fakeReplays{}
	s.Replays = fr

	vCookie, vCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	if rec := postJSON(t, mux, vCookie, vCSRF, "/api/v1/replays", `{"recording":"R"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer start: %d", rec.Code)
	}
	if rec := postJSON(t, mux, opCookie, "", "/api/v1/replays", `{"recording":"R"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("missing csrf: %d", rec.Code)
	}
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/replays", `{bad json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", rec.Code)
	}
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/replays", `{"recording":"../etc"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid request: %d %s", rec.Code, rec.Body.String())
	}
	rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/replays", `{"recording":"R","config_version":3}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	run := decodeData(t, rec)["run"].(map[string]any)
	if run["id"] != "RP1" || run["actor"] != "u-operator" || run["total"] != float64(1) {
		t.Fatalf("run = %v", run)
	}
	fr.busy = true
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/replays", `{"recording":"R"}`); rec.Code != http.StatusConflict {
		t.Fatalf("busy: %d", rec.Code)
	}
	if rec := getWith(t, mux, vCookie, "/api/v1/replays"); rec.Code != http.StatusOK || len(decodeData(t, rec)["runs"].([]any)) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := getWith(t, mux, vCookie, "/api/v1/replays/RP1"); rec.Code != http.StatusOK || decodeData(t, rec)["run"].(map[string]any)["opportunities"] != float64(42) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	if rec := getWith(t, mux, vCookie, "/api/v1/replays/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("get unknown: %d", rec.Code)
	}

	// The already-created run had no speed set, so its GET response
	// carries no "notes" key at all — honest emptiness (review P3(j) test
	// below covers the non-empty case).
	if _, ok := decodeData(t, getWith(t, mux, vCookie, "/api/v1/replays/RP1"))["notes"]; ok {
		t.Fatal("a run with no speed set must not carry a notes key")
	}
}

// TestReplaySpeedNoOpNoteSurfacesInResponse is the review P3(j)
// regression: replay.Request.Speed is documented as a no-op in the Go
// source, which a console developer consuming the HTTP API would never
// read. A request that actually sets speed must carry that caveat in
// the response payload itself, on both the create (POST) and the
// detail (GET) routes.
func TestReplaySpeedNoOpNoteSurfacesInResponse(t *testing.T) {
	s, mux := newTestServer(t)
	fr := &fakeReplays{}
	s.Replays = fr
	opCookie, opCSRF := login(t, mux, "op@example.test", "op-pw")
	vCookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/replays", `{"recording":"R","speed":2.5}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	notes, ok := decodeData(t, rec)["notes"].([]any)
	if !ok || len(notes) == 0 {
		t.Fatalf("POST response missing notes for a non-default speed: %s", rec.Body.String())
	}
	if !contains(notes, speedNoOpNote) {
		t.Fatalf("POST notes = %v, want %q", notes, speedNoOpNote)
	}

	getRec := getWith(t, mux, vCookie, "/api/v1/replays/RP1")
	if getRec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", getRec.Code, getRec.Body.String())
	}
	getNotes, ok := decodeData(t, getRec)["notes"].([]any)
	if !ok || !contains(getNotes, speedNoOpNote) {
		t.Fatalf("GET response missing the speed no-op note: %s", getRec.Body.String())
	}
}

// TestReplayStartNotReadyIs503 is the review P3(h) regression: the
// runner refusing because its Run() component has not pinned a lifetime
// context yet (jobrun.ErrNotStarted) must surface as 503 not_ready, not
// the generic 400 invalid_request every other Start error gets — it is
// a transient startup race, not a malformed request.
func TestReplayStartNotReadyIs503(t *testing.T) {
	s, mux := newTestServer(t)
	s.Replays = &fakeReplays{notStarted: true}
	opCookie, opCSRF := login(t, mux, "op@example.test", "op-pw")

	rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/replays", `{"recording":"R"}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not_ready") {
		t.Fatalf("start while not ready = %d: %s, want 503 not_ready", rec.Code, rec.Body.String())
	}
}

func contains(vals []any, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}
