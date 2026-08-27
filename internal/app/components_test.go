package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
)

// TestBuildComponentsNeverDuplicatesEngine locks in D4: the engine is no
// longer an independent app.Component (the supervisor re-enters its Run
// across a restart); appending both would give two concurrent
// Engine.Run loops racing on the same *Engine value.
func TestBuildComponentsNeverDuplicatesEngine(t *testing.T) {
	cfg := config.Bootstrap{
		Mode:           config.ModePaper,
		HTTPAddr:       ":0",
		Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
		StartingAssets: []string{"USDT"},
		PaperBalance:   "10000",
		ShutdownGrace:  time.Second,
	}
	for _, p := range []Profile{ProfileFull, ProfileScanner, ProfileAPI} {
		comps := BuildComponents(cfg, testLogger(), p)
		names := map[string]int{}
		for _, c := range comps {
			names[c.Name()]++
		}
		if names["engine"] != 0 {
			t.Fatalf("profile %s: engine must not be an independent Component (D4); got %d", p, names["engine"])
		}
		wantSupervisor := 0
		if p == ProfileFull || p == ProfileScanner {
			wantSupervisor = 1
		}
		if names["engine-supervisor"] != wantSupervisor {
			t.Fatalf("profile %s: engine-supervisor count = %d, want %d", p, names["engine-supervisor"], wantSupervisor)
		}
		wantAPI := 0
		if p == ProfileFull || p == ProfileAPI {
			wantAPI = 1
		}
		if names["api"] != wantAPI {
			t.Fatalf("profile %s: api count = %d, want %d", p, names["api"], wantAPI)
		}
	}
}

// brokenPlatformStore is a platform.Store whose Active() always fails
// (simulating a database that is reachable at storage.Open time but
// errors on the actual query — a permissions problem, a missing table
// after a partial migration, a network blip mid-boot).
type brokenPlatformStore struct{ err error }

func (b brokenPlatformStore) Insert(context.Context, string, json.RawMessage, json.RawMessage, int64) (int64, time.Time, error) {
	return 0, time.Time{}, b.err
}
func (b brokenPlatformStore) Active(context.Context) (platform.Snapshot, bool, error) {
	return platform.Snapshot{}, false, b.err
}
func (b brokenPlatformStore) Get(context.Context, int64) (platform.Snapshot, error) {
	return platform.Snapshot{}, b.err
}
func (b brokenPlatformStore) List(context.Context, int) ([]platform.VersionInfo, error) {
	return nil, b.err
}

// TestNewPlatformServiceLoadFailureIsHardError is the P2-4 regression
// test: a platform.Store that fails to load must surface the error to
// the caller (which BuildComponents turns into a hard boot failure,
// mirroring storage.Open's policy), not silently fall back to a fresh,
// env-reseeded MemoryStore that discards whatever was actually
// persisted.
func TestNewPlatformServiceLoadFailureIsHardError(t *testing.T) {
	wantErr := errors.New("connection reset by peer")
	st := brokenPlatformStore{err: wantErr}
	cfg := config.Bootstrap{
		Mode:           config.ModePaper,
		Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
		StartingAssets: []string{"USDT"},
		PaperBalance:   "10000",
	}
	svc, err := newPlatformService(testLogger(), st, nil, cfg)
	if err == nil {
		t.Fatal("expected the Load failure to propagate")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error does not wrap the underlying failure: %v", err)
	}
	if svc != nil {
		t.Fatal("expected a nil service on failure, not a silently-substituted in-memory one")
	}
}
