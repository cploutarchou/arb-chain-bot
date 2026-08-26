package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/replay"
)

type fakeReplays struct {
	runs []replay.Run
	busy bool
}

func (f *fakeReplays) Start(req replay.Request, actor string) (replay.Run, error) {
	if f.busy {
		return replay.Run{}, replay.ErrBusy
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
}
