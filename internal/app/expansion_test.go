package app

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
)

// TestSupervisorRestartAppliesModeFromSettings is settings-expansion §7's
// internal/app case: boot in MARKET_DATA (no paper engine), apply a
// document whose platform.mode is PAPER, observe the pending reason
// naming the transition, restart, and assert the engine now RUNS in
// PAPER with a paper engine and a minted session — the mode came from
// the settings document, never from cfg.Mode.
func TestSupervisorRestartAppliesModeFromSettings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(exchangeInfoFixtureWide))
	}))
	t.Cleanup(srv.Close)

	cfg := config.Bootstrap{
		Mode:           config.ModeMarketData,
		ShutdownGrace:  3 * time.Second,
		Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
		StartingAssets: []string{"USDT"},
		PaperBalance:   "10000",
	}
	engine := NewEngine(cfg, testLogger())
	engine.RESTHost = srv.URL
	engine.WSHost = "ws://127.0.0.1:1"

	svc := platform.NewService(platform.NewMemoryStore(), testLogger(), nil)
	svc.Catalog = engineCatalog{e: engine}
	ctx := context.Background()
	if _, err := svc.Load(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	engine.ApplySettings(svc.Current().Settings, svc.Current().Version)
	if engine.Mode() != config.ModeMarketData {
		t.Fatalf("configured mode before run = %s", engine.Mode())
	}

	sup := &Supervisor{
		Engine: engine, Settings: svc, Grace: 3 * time.Second, Log: testLogger(),
		Ready:     func() bool { return engine.Status().Ready },
		SessionID: engine.SessionID,
		Paper:     engine.Paper,
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sup.Run(runCtx) }()
	t.Cleanup(func() { cancelRun(); <-done })

	waitForSupervisorState(t, sup, StateReady)
	if engine.Mode() != config.ModeMarketData || engine.Paper() != nil || engine.Status().Paper != nil {
		t.Fatalf("MARKET_DATA run must have no paper engine: mode=%s paper=%v", engine.Mode(), engine.Paper())
	}

	next := svc.Current().Settings.Clone()
	next.Platform.Mode = config.ModePaper
	v := next.Venues["binance"]
	v.PaperEnabled = true
	next.Venues["binance"] = v
	if _, err := svc.Apply(ctx, "test", "test", next); err != nil {
		t.Fatalf("apply PAPER: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := sup.Status()
		if len(st.PendingReasons) > 0 && strings.Contains(strings.Join(st.PendingReasons, ";"), "mode MARKET_DATA→PAPER") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending reason never named the mode transition: %v", st.PendingReasons)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Not applied until the restart: still running MARKET_DATA.
	if engine.Mode() != config.ModeMarketData {
		t.Fatalf("mode changed without a restart: %s", engine.Mode())
	}

	reqDone := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "test", Reason: "enter paper", Done: reqDone}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reqDone:
		if err != nil {
			t.Fatalf("restart failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("restart never completed")
	}
	waitForSupervisorState(t, sup, StateReady)
	if engine.Mode() != config.ModePaper {
		t.Fatalf("running mode after restart = %s, want PAPER", engine.Mode())
	}
	if engine.Paper() == nil || engine.Status().Paper == nil {
		t.Fatal("PAPER run must construct the paper engine")
	}
	if engine.SessionID() == "" {
		t.Fatal("PAPER run must mint a paper session")
	}
	if st := sup.Status(); st.SettingsVersion != 2 || len(st.PendingReasons) != 0 {
		t.Fatalf("status after restart = %+v", st)
	}
}

// TestSetLogLevelChangesEmittedRecords: the package-scoped LevelVar
// gates records at runtime; SetLogLevel is what the platform.log_level
// subscriber calls.
func TestSetLogLevelChangesEmittedRecords(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newHandler(&buf, &logLevel))
	t.Cleanup(func() { _ = SetLogLevel("info") })

	if err := SetLogLevel("error"); err != nil {
		t.Fatal(err)
	}
	log.Info("suppressed-info")
	log.Warn("suppressed-warn")
	log.Error("kept-error")
	if out := buf.String(); strings.Contains(out, "suppressed") || !strings.Contains(out, "kept-error") {
		t.Fatalf("error level: %q", out)
	}
	if CurrentLogLevel() != "error" {
		t.Fatalf("CurrentLogLevel = %q", CurrentLogLevel())
	}
	buf.Reset()
	if err := SetLogLevel("debug"); err != nil {
		t.Fatal(err)
	}
	log.Debug("kept-debug", "token", "should-not-appear")
	out := buf.String()
	if !strings.Contains(out, "kept-debug") {
		t.Fatalf("debug level: %q", out)
	}
	if strings.Contains(out, "should-not-appear") {
		t.Fatalf("redaction lost at debug level: %q", out)
	}
	if err := SetLogLevel("verbose"); err == nil {
		t.Fatal("unknown level must be rejected")
	}
	if CurrentLogLevel() != "debug" {
		t.Fatalf("bad SetLogLevel changed the level: %q", CurrentLogLevel())
	}
}

// TestAllowSetDisabledMutesCommandsAndPushesTogether: telegram.disabled
// makes Allowed() false AND IDs() nil (the shared-set invariant), while
// the zero-value TelegramSettings leaves both live (D3).
func TestAllowSetDisabledMutesCommandsAndPushesTogether(t *testing.T) {
	zero := platform.TelegramSettings{Allowlist: []int64{7, 8}}
	a := &allowSet{}
	a.Set(zero.Allowlist)
	a.SetDisabled(zero.Disabled)
	if !a.Allowed(7) || len(a.IDs()) != 2 || a.Disabled() {
		t.Fatalf("zero-value telegram settings must deliver: allowed=%v ids=%v", a.Allowed(7), a.IDs())
	}
	a.SetDisabled(true)
	if a.Allowed(7) || a.IDs() != nil {
		t.Fatalf("disabled must mute both: allowed=%v ids=%v", a.Allowed(7), a.IDs())
	}
	if got := a.Members(); len(got) != 2 {
		t.Fatalf("Members (status view) must still list the configured ids: %v", got)
	}
	a.SetDisabled(false)
	if !a.Allowed(8) || len(a.IDs()) != 2 {
		t.Fatal("re-enable must restore delivery")
	}
}

// TestSupervisorPendingReasonNamesModeTransition uses the fake engine:
// a mode-only change raises a specific reason; another restart-scoped
// change keeps the generic one.
func TestSupervisorPendingReasonNamesModeTransition(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	cur := sup.Settings.Current().Settings.Clone()
	cur.Platform.Mode = config.ModeRecord
	if _, err := sup.Settings.Apply(context.Background(), "alice", "web", cur); err != nil {
		t.Fatal(err)
	}
	waitForPendingReason(t, sup, "platform settings v2: mode PAPER→RECORD")

	cur = sup.Settings.Current().Settings.Clone()
	cur.Platform.LogLevel = "debug" // hot: must not change the banner
	if _, err := sup.Settings.Apply(context.Background(), "alice", "web", cur); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if st := sup.Status(); len(st.PendingReasons) != 1 || !strings.Contains(st.PendingReasons[0], "mode PAPER→RECORD") {
		t.Fatalf("hot change altered the pending reason: %v", st.PendingReasons)
	}
	cancel()
	<-done
}

// TestBuildComponentsAlwaysConstructsAIScheduler pins D4: with no key
// and no provider the scheduler still exists in engine profiles.
func TestBuildComponentsAlwaysConstructsAIScheduler(t *testing.T) {
	cfg := config.Bootstrap{
		Mode: config.ModeMarketData, HTTPAddr: ":0",
		Symbols: []string{"BTCUSDT", "ETHUSDT", "ETHBTC"}, StartingAssets: []string{"USDT"},
		PaperBalance: "10000", ShutdownGrace: time.Second, AIModel: "claude-sonnet-5",
	}
	names := map[string]int{}
	for _, c := range BuildComponents(cfg, testLogger(), ProfileScanner) {
		names[c.Name()]++
	}
	if names["ai-scheduler"] != 1 {
		t.Fatalf("ai-scheduler must always be built in engine profiles (D4): %v", names)
	}
}

// TestBuildAdvisorResolvesKeyThroughSecrets: with the anthropic provider
// enabled and nothing resolvable, the advisor stays idle with the named
// reason; when the secret chain resolves, the provider is built with the
// budget's max tokens; the fake provider needs no secret at all.
func TestBuildAdvisorResolvesKeyThroughSecrets(t *testing.T) {
	base := platform.AISettings{
		Enabled: true, Provider: "anthropic", Model: "claude-sonnet-5",
		Budget: platform.AIBudget{MaxAnalysesPerDay: 10, MaxOutputTokens: 512},
	}
	src := fakeSecretSource{}
	adv, st := buildAdvisor(base, testLogger(), src)
	if adv != nil || st.Running || st.Reason != "no anthropic_api_key" || !st.Enabled {
		t.Fatalf("no key: adv=%v st=%+v", adv, st)
	}
	src["anthropic_api_key"] = "vault-key-value-1234567890"
	adv, st = buildAdvisor(base, testLogger(), src)
	if adv == nil || !st.Running || st.KeySource != "vault" || adv.Model() != "claude-sonnet-5" {
		t.Fatalf("with key: adv=%v st=%+v", adv, st)
	}
	base.Enabled = false
	if adv, st := buildAdvisor(base, testLogger(), src); adv != nil || st.Reason != "disabled in settings" {
		t.Fatalf("disabled: adv=%v st=%+v", adv, st)
	}
	base.Enabled, base.Provider = true, "fake"
	if adv, st := buildAdvisor(base, testLogger(), fakeSecretSource{}); adv == nil || !st.Running {
		t.Fatalf("fake: adv=%v st=%+v", adv, st)
	}
}

type fakeSecretSource map[string]string

func (f fakeSecretSource) Get(_ context.Context, name string) (string, string, bool) {
	v, ok := f[name]
	return v, "vault", ok && v != ""
}
