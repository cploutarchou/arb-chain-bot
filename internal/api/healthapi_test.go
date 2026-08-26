package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
)

// TestSystemHealthWithoutEngineStillAnswers covers BL-18's dropped
// needEngine gate: an API-profile process (no engine at all) must still
// serve process stats rather than 404ing the whole route.
func TestSystemHealthWithoutEngineStillAnswers(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	rec := getWith(t, mux, cookie, "/api/v1/system/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("health without engine = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	proc, ok := env.Data["process"].(map[string]any)
	if !ok {
		t.Fatalf("process stats missing: %+v", env.Data)
	}
	if _, ok := proc["goroutines"]; !ok {
		t.Fatalf("process stats incomplete: %+v", proc)
	}
	for _, absent := range []string{"database", "restart", "engine"} {
		if _, ok := env.Data[absent]; ok {
			t.Errorf("%q present without its source component: %v", absent, env.Data[absent])
		}
	}
}

// fakeRecorderController is the minimal RecorderController fake for the
// health test (opsapi_test.go's fakes are unexported to that file).
type fakeRecorderControllerHealth struct{ status marketdata.RecorderStatus }

func (f fakeRecorderControllerHealth) StartSession() (string, error) { return "", nil }
func (f fakeRecorderControllerHealth) Stop() (string, error)         { return "", nil }
func (f fakeRecorderControllerHealth) Status() marketdata.RecorderStatus {
	return f.status
}

func TestSystemHealthMergesEveryPresentSource(t *testing.T) {
	s, mux := newTestServer(t)
	s.Reads = fakeReads{portfolio: true}
	s.Recorder = fakeRecorderControllerHealth{status: marketdata.RecorderStatus{
		Running: true, QueueDepth: 3, QueueCapacity: 8192,
	}}
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	rec := getWith(t, mux, cookie, "/api/v1/system/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("health = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Engine-derived field (from fakeReads.Health()) merged at top level.
	if !strings.Contains(body, `"ready":true`) {
		t.Fatalf("engine health not merged: %s", body)
	}
	// Recorder queue depth nested under queues.recorder.
	if !strings.Contains(body, `"recorder":{"capacity":8192,"depth":3}`) {
		t.Fatalf("recorder queue depth missing: %s", body)
	}
	if !strings.Contains(body, `"process":{`) {
		t.Fatalf("process stats missing: %s", body)
	}
}
