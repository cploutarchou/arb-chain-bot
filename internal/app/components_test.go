package app

import (
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
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
