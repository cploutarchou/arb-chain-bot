package platform

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// TestValidateModeTable is settings-expansion §7: LIVE in any case gets
// its own named refusal, REPLAY/BACKTEST are named as batch runs, SHADOW
// is enumerated-but-unavailable, garbage is unknown.
func TestValidateModeTable(t *testing.T) {
	cases := []struct {
		mode config.Mode
		want string // substring of the error; "" = must validate
	}{
		{config.ModeMarketData, ""},
		{config.ModeRecord, ""},
		{config.ModePaper, ""},
		{"LIVE", "live trading is permanently disabled"},
		{"live", "live trading is permanently disabled"},
		{"Live", "live trading is permanently disabled"},
		{config.ModeReplay, "batch run"},
		{config.ModeBacktest, "batch run"},
		{config.ModeShadow, "not wired into Engine.Run"},
		{"", "required"},
		{"garbage", "not an operating mode"},
	}
	for _, tc := range cases {
		s := validSettings()
		s.Platform.Mode = tc.mode
		err := s.Validate()
		if tc.want == "" {
			if err != nil {
				t.Errorf("mode %q: unexpected error %v", tc.mode, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("mode %q: error %v, want substring %q", tc.mode, err, tc.want)
		}
	}
}

// TestModeTableAgreesWithValidate: every config.Mode constant is either
// a settable table entry (Validate accepts) or not (Validate rejects) —
// adding a constant without a table decision fails here.
func TestModeTableAgreesWithValidate(t *testing.T) {
	all := []config.Mode{config.ModeMarketData, config.ModeRecord, config.ModeReplay, config.ModeBacktest, config.ModePaper, config.ModeShadow}
	for _, m := range all {
		s := validSettings()
		s.Platform.Mode = m
		if m == config.ModePaper {
			v := s.Venues["binance"]
			v.PaperEnabled = true
			s.Venues["binance"] = v
		}
		err := s.Validate()
		if Settable(m) != (err == nil) {
			t.Errorf("mode %s: Settable=%v but Validate err=%v", m, Settable(m), err)
		}
	}
	for _, p := range ModeTable() {
		if string(p.ID) == "LIVE" {
			t.Fatal("LIVE must never appear in ModeTable")
		}
		if !p.Available && p.Reason == "" {
			t.Fatalf("mode %s: unavailable without a reason", p.ID)
		}
	}
	if Settable("LIVE") {
		t.Fatal("LIVE must never be settable")
	}
}

func TestAISettingsBounds(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*AISettings)
		ok   bool
	}{
		{"openai not built", func(a *AISettings) { a.Provider = "openai" }, false},
		{"empty provider", func(a *AISettings) { a.Provider = "" }, false},
		{"fake ok", func(a *AISettings) { a.Provider = "fake" }, true},
		{"empty model", func(a *AISettings) { a.Model = "" }, false},
		{"model bad chars", func(a *AISettings) { a.Model = "claude sonnet" }, false},
		{"model too long", func(a *AISettings) { a.Model = strings.Repeat("a", 65) }, false},
		{"hourly zero disables", func(a *AISettings) { a.Schedule.HourlyMinutes = 0 }, true},
		{"hourly below 15", func(a *AISettings) { a.Schedule.HourlyMinutes = 14 }, false},
		{"hourly 15", func(a *AISettings) { a.Schedule.HourlyMinutes = 15 }, true},
		{"hourly 1440", func(a *AISettings) { a.Schedule.HourlyMinutes = 1440 }, true},
		{"hourly 1441", func(a *AISettings) { a.Schedule.HourlyMinutes = 1441 }, false},
		{"daily zero", func(a *AISettings) { a.Schedule.DailyHours = 0 }, true},
		{"daily 1", func(a *AISettings) { a.Schedule.DailyHours = 1 }, true},
		{"daily 168", func(a *AISettings) { a.Schedule.DailyHours = 168 }, true},
		{"daily 169", func(a *AISettings) { a.Schedule.DailyHours = 169 }, false},
		{"daily negative", func(a *AISettings) { a.Schedule.DailyHours = -1 }, false},
		{"weekly zero", func(a *AISettings) { a.Schedule.WeeklyHours = 0 }, true},
		{"weekly 23", func(a *AISettings) { a.Schedule.WeeklyHours = 23 }, false},
		{"weekly 24", func(a *AISettings) { a.Schedule.WeeklyHours = 24 }, true},
		{"weekly 720", func(a *AISettings) { a.Schedule.WeeklyHours = 720 }, true},
		{"weekly 721", func(a *AISettings) { a.Schedule.WeeklyHours = 721 }, false},
		{"per day 0", func(a *AISettings) { a.Budget.MaxAnalysesPerDay = 0 }, false},
		{"per day 1", func(a *AISettings) { a.Budget.MaxAnalysesPerDay = 1 }, true},
		{"per day 96", func(a *AISettings) { a.Budget.MaxAnalysesPerDay = 96 }, true},
		{"per day 97", func(a *AISettings) { a.Budget.MaxAnalysesPerDay = 97 }, false},
		{"tokens 255", func(a *AISettings) { a.Budget.MaxOutputTokens = 255 }, false},
		{"tokens 256", func(a *AISettings) { a.Budget.MaxOutputTokens = 256 }, true},
		{"tokens 8192", func(a *AISettings) { a.Budget.MaxOutputTokens = 8192 }, true},
		{"tokens 8193", func(a *AISettings) { a.Budget.MaxOutputTokens = 8193 }, false},
		{"enabled anthropic without key is accepted (warning at apply)", func(a *AISettings) { a.Enabled = true }, true},
	}
	for _, tc := range cases {
		s := validSettings()
		tc.mut(&s.AI)
		err := s.Validate()
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: expected a validation error", tc.name)
		}
	}
}

func TestLogLevelAndOriginValidators(t *testing.T) {
	for _, lvl := range []string{"debug", "info", "warn", "error"} {
		if err := ValidateLogLevel(lvl); err != nil {
			t.Errorf("level %q: %v", lvl, err)
		}
	}
	for _, lvl := range []string{"", "INFO", "trace", "warning"} {
		if err := ValidateLogLevel(lvl); err == nil {
			t.Errorf("level %q: expected error", lvl)
		}
	}
	good := []string{"http://localhost:3000", "https://console.example.com", "https://console.example.com:8443", "http://10.0.0.5"}
	for _, o := range good {
		if err := ValidateOrigin(o); err != nil {
			t.Errorf("origin %q: %v", o, err)
		}
	}
	bad := []string{"", "localhost:3000", "ftp://x", "http://*.example.com", "https://a.com/path", "https://a.com/", "https://a.com?x=1", "https://a.com#f", "https://user:pw@a.com", "http://" + strings.Repeat("a", 260) + ".com", "https://"}
	for _, o := range bad {
		if err := ValidateOrigin(o); err == nil {
			t.Errorf("origin %q: expected error", o)
		}
	}
}

// preExpansionPayload is a raw JSON literal a T-057-era row would hold:
// no "platform", no "ai", and a "telegram" section carrying ONLY the
// allowlist. A round-tripped struct would carry the new keys with zero
// values and would not exercise the branches that matter (design §7).
const preExpansionPayload = `{
  "venues": {"binance": {"enabled": true, "paper_enabled": true,
    "symbols": ["BTCUSDT","ETHBTC","ETHUSDT"], "starting_assets": ["USDT"],
    "fees": {"maker_bps": "10", "taker_bps": "10", "token_discount": false}}},
  "paper": {"balances": {"USDT": "10000"}},
  "telegram": {"allowlist": [111]}
}`

func TestWithDefaultsUpgradesRawPreExpansionPayload(t *testing.T) {
	var doc Settings
	if err := json.Unmarshal([]byte(preExpansionPayload), &doc); err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err == nil {
		t.Fatal("a raw pre-expansion payload must NOT validate before WithDefaults — otherwise this test proves nothing")
	}
	cfg := testCfg()
	cfg.AIModel = "claude-sonnet-5"
	up := doc.WithDefaults(cfg)
	if err := up.Validate(); err != nil {
		t.Fatalf("WithDefaults must yield a valid document: %v", err)
	}
	if up.Platform.Mode != config.ModePaper || up.Platform.LogLevel != "info" || up.Platform.AllowedOrigin != "http://localhost:3000" {
		t.Fatalf("platform section not seeded: %+v", up.Platform)
	}
	if up.AI.Provider != "anthropic" || up.AI.Model != "claude-sonnet-5" || up.AI.Enabled {
		t.Fatalf("ai section not seeded: %+v", up.AI)
	}
	// D3's quiet-failure case: a telegram section with only an allowlist
	// keeps delivering.
	if up.Telegram.Disabled {
		t.Fatal("telegram.disabled must stay false on an upgraded document (delivery stays on)")
	}
	if len(up.Telegram.Allowlist) != 1 || up.Telegram.Allowlist[0] != 111 {
		t.Fatalf("allowlist lost on upgrade: %v", up.Telegram.Allowlist)
	}
	// Idempotent and non-destructive on an already-complete document.
	full := validSettings()
	full.Platform.LogLevel = "debug"
	full.AI.Model = "custom-model"
	again := full.WithDefaults(cfg)
	if again.Platform.LogLevel != "debug" || again.AI.Model != "custom-model" {
		t.Fatalf("WithDefaults overwrote set fields: %+v %+v", again.Platform, again.AI)
	}
}

// rawStore is a platform.Store that hands back a raw pre-expansion JSON
// payload exactly as the database would, bypassing Insert's struct
// round-trip so Load/Get/Rollback see the real upgrade case.
type rawStore struct {
	*MemoryStore
	raw map[int64]json.RawMessage
}

func (r *rawStore) Active(ctx context.Context) (Snapshot, bool, error) {
	snap, ok, err := r.MemoryStore.Active(ctx)
	if err != nil || !ok {
		return snap, ok, err
	}
	return r.override(snap)
}

func (r *rawStore) Get(ctx context.Context, version int64) (Snapshot, error) {
	snap, err := r.MemoryStore.Get(ctx, version)
	if err != nil {
		return snap, err
	}
	snap, _, err = r.override(snap)
	return snap, err
}

func (r *rawStore) override(snap Snapshot) (Snapshot, bool, error) {
	if raw, ok := r.raw[snap.Version]; ok {
		var doc Settings
		if err := json.Unmarshal(raw, &doc); err != nil {
			return Snapshot{}, false, err
		}
		snap.Settings = doc
	}
	return snap, true, nil
}

func TestServiceNormalizesPreExpansionRowOnLoadGetAndRollback(t *testing.T) {
	ctx := context.Background()
	st := &rawStore{MemoryStore: NewMemoryStore(), raw: map[int64]json.RawMessage{}}
	// Persist v1 as the raw literal (Insert only needs SOME payload; the
	// override makes every read of v1 return the pre-expansion bytes).
	if _, _, err := st.Insert(ctx, "", json.RawMessage(preExpansionPayload), nil, 0); err != nil {
		t.Fatal(err)
	}
	st.raw[1] = json.RawMessage(preExpansionPayload)

	svc := NewService(st, testLogger(), nil)
	snap, err := svc.Load(ctx, testCfg())
	if err != nil {
		t.Fatalf("Load must upgrade in memory rather than refuse to boot: %v", err)
	}
	if snap.Settings.Platform.Mode != config.ModePaper || snap.Settings.AI.Provider != "anthropic" {
		t.Fatalf("Load did not normalize: %+v", snap.Settings.Platform)
	}
	// Load never writes a version (D3): still exactly one row.
	if list, _ := st.List(ctx, 10); len(list) != 1 {
		t.Fatalf("Load wrote a version: %d rows", len(list))
	}

	got, err := svc.Get(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings.Platform.Mode == "" || got.Settings.AI.Model == "" {
		t.Fatalf("Get did not normalize: %+v", got.Settings)
	}

	// A later complete version, then a rollback TO the raw v1 must
	// validate (it goes through Get → WithDefaults) and persist a
	// complete document.
	next := svc.Current().Settings
	next.Platform.LogLevel = "debug"
	if _, err := svc.Apply(ctx, "alice", "web", next); err != nil {
		t.Fatal(err)
	}
	rb, err := svc.Rollback(ctx, "alice", "web", 1)
	if err != nil {
		t.Fatalf("rollback to the pre-expansion row must normalize first: %v", err)
	}
	if rb.Settings.Platform.LogLevel != "info" || rb.Settings.Platform.Mode != config.ModePaper {
		t.Fatalf("rollback did not carry defaults: %+v", rb.Settings.Platform)
	}
	if rb.Settings.Telegram.Disabled {
		t.Fatal("rollback must not mute telegram")
	}
}

func TestSeedMapsNonSettableModesToMarketData(t *testing.T) {
	for _, m := range []config.Mode{config.ModeReplay, config.ModeBacktest, config.ModeShadow} {
		cfg := testCfg()
		cfg.Mode = m
		s := Seed(cfg)
		if s.Platform.Mode != config.ModeMarketData {
			t.Errorf("ARB_MODE=%s: seeded platform.mode = %s, want MARKET_DATA", m, s.Platform.Mode)
		}
		if s.Venues["binance"].PaperEnabled {
			t.Errorf("ARB_MODE=%s: paper_enabled must follow the seeded mode, not the env one", m)
		}
		if err := s.Validate(); err != nil {
			t.Errorf("ARB_MODE=%s: seed must validate: %v", m, err)
		}
		var doc Settings
		_ = json.Unmarshal([]byte(preExpansionPayload), &doc)
		if up := doc.WithDefaults(cfg); up.Platform.Mode != config.ModeMarketData {
			t.Errorf("ARB_MODE=%s: WithDefaults mode = %s", m, up.Platform.Mode)
		}
		if _, note := SeedMode(m); note == "" {
			t.Errorf("ARB_MODE=%s: SeedMode must name the substitution", m)
		}
	}
	for _, m := range []config.Mode{config.ModeMarketData, config.ModeRecord, config.ModePaper} {
		if got, note := SeedMode(m); got != m || note != "" {
			t.Errorf("settable %s: SeedMode = %s, %q", m, got, note)
		}
	}
}

func TestSeedAIReproducesEnvBehaviour(t *testing.T) {
	cfg := testCfg()
	if s := Seed(cfg); s.AI.Enabled {
		t.Fatal("no key, no provider: advisor must seed disabled")
	}
	cfg.AnthropicAPIKey = "k"
	if s := Seed(cfg); !s.AI.Enabled || s.AI.Provider != "anthropic" {
		t.Fatalf("key set: %+v", s.AI)
	}
	cfg.AnthropicAPIKey = ""
	cfg.AIProvider = "fake"
	if s := Seed(cfg); !s.AI.Enabled || s.AI.Provider != "fake" {
		t.Fatalf("fake provider: %+v", s.AI)
	}
	cfg.AIProvider = "openai"
	if s := Seed(cfg); s.AI.Enabled || s.AI.Provider != "anthropic" {
		t.Fatalf("unknown provider must seed disabled with a settable provider: %+v", s.AI)
	}
	if err := Seed(cfg).Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestEverySectionHasExplicitPermission reflects over Settings' json
// tags so a future top-level section cannot land without a decision in
// PermissionForSection (design D2).
func TestEverySectionHasExplicitPermission(t *testing.T) {
	explicit := map[string]auth.Permission{
		"venues": auth.PermExchangeConfig, "platform": auth.PermSystemConfig,
		"paper": auth.PermSystemConfig, "telegram": auth.PermSystemConfig, "ai": auth.PermSystemConfig,
	}
	rt := reflect.TypeOf(Settings{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		want, ok := explicit[tag]
		if !ok {
			t.Fatalf("section %q has no explicit PermissionForSection decision in this test", tag)
		}
		if got := PermissionForSection(tag); got != want {
			t.Fatalf("section %q → %s, want %s", tag, got, want)
		}
	}
	if got := PermissionForSection("never-heard-of-it"); got != auth.PermSystemConfig {
		t.Fatalf("unknown section must fail closed to system:config, got %s", got)
	}
}

func TestHotAndRestartPaths(t *testing.T) {
	hot := []string{"platform.log_level", "platform.allowed_origin", "ai.enabled", "ai.schedule.hourly_minutes", "telegram.disabled", "telegram.allowlist"}
	for _, p := range hot {
		if !IsHot(p) {
			t.Errorf("%s must be hot", p)
		}
	}
	restart := []string{"platform.mode", "venues.binance.symbols", "paper.balances.USDT"}
	for _, p := range restart {
		if IsHot(p) {
			t.Errorf("%s must be restart-scoped", p)
		}
	}
	if validSettings().RestartScoped(map[string]strategy.Change{"ai.model": {Old: "a", New: "b"}}) {
		t.Fatal("ai-only diff must not be restart-scoped")
	}
	if !validSettings().RestartScoped(map[string]strategy.Change{"platform.mode": {Old: "PAPER", New: "RECORD"}}) {
		t.Fatal("mode diff must be restart-scoped")
	}
	ft := FieldTiming(validSettings(), true)
	for path, want := range map[string]string{
		"platform.mode": "restart", "platform.log_level": "hot", "platform.allowed_origin": "hot",
		"ai.provider": "hot", "ai.budget.max_output_tokens": "hot", "telegram.disabled": "hot",
	} {
		if ft[path] != want {
			t.Errorf("field_timing[%s] = %q, want %q", path, ft[path], want)
		}
	}
	for path, timing := range ft {
		if timing != "hot" && timing != "restart" {
			t.Errorf("field_timing[%s] = %q: must stay two-valued", path, timing)
		}
	}
}

func TestErrConnectorUnavailableSurvivesApply(t *testing.T) {
	svc := NewService(NewMemoryStore(), testLogger(), nil)
	if _, err := svc.Load(context.Background(), testCfg()); err != nil {
		t.Fatal(err)
	}
	bad := validSettings()
	bad.Venues["bybit"] = bad.Venues["binance"]
	_, err := svc.Apply(context.Background(), "alice", "web", bad)
	if !errors.Is(err, ErrConnectorUnavailable) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("both sentinels must be reachable: %v", err)
	}
}
