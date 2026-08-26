package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/campaign"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
)

type fakeRecorder struct {
	running bool
	id      string
}

func (f *fakeRecorder) StartSession() (string, error) {
	if f.running {
		return "", marketdata.ErrRecorderRunning
	}
	f.running, f.id = true, "SESSION1"
	return f.id, nil
}

func (f *fakeRecorder) Stop() (string, error) {
	if !f.running {
		return "", marketdata.ErrRecorderIdle
	}
	f.running = false
	return f.id, nil
}

func (f *fakeRecorder) Status() marketdata.RecorderStatus {
	return marketdata.RecorderStatus{Running: f.running, SessionID: f.id}
}

type fakeCampaigns struct {
	runs []campaign.Run
	busy bool
}

func (f *fakeCampaigns) Start(req campaign.Request, actor string) (campaign.Run, error) {
	if f.busy {
		return campaign.Run{}, campaign.ErrBusy
	}
	req, err := req.Normalize()
	if err != nil {
		return campaign.Run{}, err
	}
	run := campaign.Run{ID: "RUN1", Recording: req.Recording, Request: req, Status: campaign.StatusQueued, Actor: actor, Total: req.Total()}
	f.runs = append(f.runs, run)
	return run, nil
}

func (f *fakeCampaigns) List(context.Context, int) ([]campaign.Run, error) { return f.runs, nil }

func (f *fakeCampaigns) Get(_ context.Context, id string) (campaign.Run, error) {
	for _, r := range f.runs {
		if r.ID == id {
			r.ReportMD = "# report"
			return r, nil
		}
	}
	return campaign.Run{}, campaign.ErrNotFound
}

func postJSON(t *testing.T, mux *http.ServeMux, cookie *http.Cookie, csrf, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.AddCookie(cookie)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Data  map[string]any `json:"data"`
		Error *APIError      `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body.String())
	}
	return env.Data
}

func TestRecorderControlRoutes(t *testing.T) {
	s, mux := newTestServer(t)
	// Absent recorder → 404, regardless of role.
	opCookie, opCSRF := login(t, mux, "op@example.test", "op-pw")
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/recordings/start", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("absent recorder: %d %s", rec.Code, rec.Body.String())
	}
	fr := &fakeRecorder{}
	s.Recorder = fr

	// Viewers can read the status but not start sessions.
	vCookie, vCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	if rec := getWith(t, mux, vCookie, "/api/v1/recordings"); rec.Code != http.StatusOK {
		t.Fatalf("viewer read: %d %s", rec.Code, rec.Body.String())
	} else if d := decodeData(t, rec); d["persistence"] != false || d["recorder"].(map[string]any)["running"] != false {
		t.Fatalf("read payload = %v", d)
	}
	if rec := postJSON(t, mux, vCookie, vCSRF, "/api/v1/recordings/start", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer start: %d", rec.Code)
	}
	// CSRF is mandatory.
	if rec := postJSON(t, mux, opCookie, "", "/api/v1/recordings/start", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("missing csrf: %d", rec.Code)
	}
	rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/recordings/start", "")
	if rec.Code != http.StatusOK || decodeData(t, rec)["session_id"] != "SESSION1" {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/recordings/start", ""); rec.Code != http.StatusConflict {
		t.Fatalf("double start: %d", rec.Code)
	}
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/recordings/stop", ""); rec.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/recordings/stop", ""); rec.Code != http.StatusConflict {
		t.Fatalf("double stop: %d", rec.Code)
	}
}

func TestCampaignRoutes(t *testing.T) {
	s, mux := newTestServer(t)
	opCookie, opCSRF := login(t, mux, "op@example.test", "op-pw")
	if rec := getWith(t, mux, opCookie, "/api/v1/campaigns"); rec.Code != http.StatusNotFound {
		t.Fatalf("absent runner: %d", rec.Code)
	}
	fc := &fakeCampaigns{}
	s.Campaigns = fc

	vCookie, vCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	if rec := postJSON(t, mux, vCookie, vCSRF, "/api/v1/campaigns", `{"recording":"R"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer start: %d", rec.Code)
	}
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/campaigns", `{bad json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", rec.Code)
	}
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/campaigns", `{"recording":"R","grid":"weird"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid request: %d %s", rec.Code, rec.Body.String())
	}
	rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/campaigns", `{"recording":"R","grid":"baseline","seeds":[1]}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	run := decodeData(t, rec)["run"].(map[string]any)
	if run["id"] != "RUN1" || run["actor"] != "u-operator" || run["total"] != float64(1) {
		t.Fatalf("run = %v", run)
	}
	fc.busy = true
	if rec := postJSON(t, mux, opCookie, opCSRF, "/api/v1/campaigns", `{"recording":"R"}`); rec.Code != http.StatusConflict {
		t.Fatalf("busy: %d", rec.Code)
	}
	if rec := getWith(t, mux, vCookie, "/api/v1/campaigns"); rec.Code != http.StatusOK || len(decodeData(t, rec)["runs"].([]any)) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := getWith(t, mux, vCookie, "/api/v1/campaigns/RUN1"); rec.Code != http.StatusOK || decodeData(t, rec)["run"].(map[string]any)["report_md"] != "# report" {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	if rec := getWith(t, mux, vCookie, "/api/v1/campaigns/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("get unknown: %d", rec.Code)
	}
}
