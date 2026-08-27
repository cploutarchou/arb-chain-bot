# Settings Expansion: Operating Mode, Secrets, Providers, Venues

Follow-on to `docs/design/platform-settings-and-restart.md` (**T-057**,
implemented: `internal/platform` versioned document, `app.Supervisor`
restart, `/api/v1/platform/*`, `/api/v1/engine/*`). That design moved
symbols, starting assets, venue enablement, fees, paper balances and the
Telegram allowlist out of the environment. This one takes the **remaining
env-only fields** that an operator legitimately needs to change without a
redeploy — `Mode`, `LogLevel`, `AllowedOrigin`, the AI provider triple, the
Telegram token — and gives each one an honest home: the versioned document,
an encrypted vault, or a documented "stays env" line.

Tracked as **T-059** (mode + providers + log level), **T-060** (secrets
vault), **T-061** (venue availability + console). Three packages, ~5-6
engineer-days total.

## 0. Hard constraints (restated, enforced by this design)

- **Live trading remains permanently disabled.** `LiveExecutor` returns
  `ErrLiveTradingDisabled`; nothing here touches it. The operating-mode
  setting enumerates exactly `MARKET_DATA`, `RECORD`, `PAPER`, `SHADOW`.
  The string `LIVE` (any case) is rejected by `Validate` with a named
  error, not treated as "unknown value" — so the enum can never grow into
  it by accident. `REPLAY`/`BACKTEST` are **batch runs**, already driven
  from the console (`replay.Runner`, `campaign.Runner`) and from
  `cmd/replay` with `ARB_REPLAY_SESSION`; they are not process modes an
  operator switches into and are rejected by the same validator.
- **No exchange trading API keys.** No table column, settings field, route
  or UI control in this design accepts, stores, masks or displays one. All
  connectors use keyless public market data. Read-only/testnet credentials,
  if a future task ever needs them, are that task's problem and require a
  security review — explicitly out of scope here. The secrets vault
  (§2) has a **closed, two-entry registry**; adding a name is a code change.
- **Risk engine and money math untouched.** No file under `internal/risk`,
  `internal/pricing`, `internal/opportunity`, `internal/simulation`,
  `internal/portfolio` or `internal/fees` is modified. bps stay decimal,
  balances stay decimal strings.
- **No guaranteed-profit language** anywhere in copy this design specifies.
- The hot path never waits on any of it: every read below is an atomic
  pointer/accessor or happens between runs.

## 1. Decisions

- **D1** Operating mode becomes `platform.mode` in the existing versioned
  document — **restart-scoped**, applied by the Supervisor on the next
  engine restart. `ARB_MODE` becomes a first-boot seed (T-057 D5 extended).
- **D2** The document grows two top-level sections, `platform` and `ai`.
  `strategy.DiffAny` flattens any struct, and
  `platform.PermissionForSection` **defaults to `PermSystemConfig`**
  (`authz.go:17-18`), so new sections fail *closed* to ADMIN. A reflect-over-
  sections test forces every future section to get an explicit mapping.
- **D3** **The JSONB upgrade rule.** Old payloads have no `platform`/`ai`
  keys, so a naive unmarshal yields `Mode == ""`, `Validate` fails, and
  `buildPlatform` takes the P2-4 hard-failure branch
  (`components.go:86`): **the process refuses to boot after the deploy.**
  `Settings.WithDefaults(cfg)` fills zero-valued new sections from the env
  seed and runs **wherever a stored payload becomes a `Settings`** —
  `Service.Load`, `Service.Get`, and the rollback path — not just at load.
  It fills in memory and never writes a version; the next operator write
  persists the complete document. The same hazard applies to a **new field
  on an existing section**, and it is worse there because it fails
  *quietly*: its zero value must equal today's behaviour. That is why §4.2
  adds `telegram.disabled`, not `telegram.enabled` — a persisted
  `{"telegram":{"allowlist":[…]}}` must keep delivering, not silently mute
  a security-relevant channel on upgrade.
- **D4** `ai.*` is **hot**, and to make that true `ai.Service` +
  `ai.Scheduler` are **always constructed**, with an `ai.Switch` holding a
  possibly-nil advisor. Today `components.go:134,170` build neither when no
  key is present, so a runtime enable would silently do nothing until a
  redeploy. Idle cost: one timer that skips. This keeps `field_timing`
  two-valued.
- **D5** `telegram.enabled` is **hot** (it mutes/unmutes an existing bot),
  but the **bot token is not**: `telegram.Bot`/`PushSink` are process
  Components with a long-poll loop, and the Supervisor restarts the engine
  only. A token written to the vault on a process that booted without one
  applies on the next **process** restart, and the API says so in the
  secrets response's `applies` field. That asymmetry is real; we surface it
  rather than pretend.
- **D6** `platform.log_level` is **hot** via a `slog.LevelVar`.
- **D7** `platform.allowed_origin` is **hot**, deviating from the brief's
  "restart". Evidence: `api/ws.go:26` is its only reader and the Supervisor
  never rebuilds `api.Server`, so "restart" would mean "redeploy" and the
  chip would lie. One atomic accessor makes it honest. Safety bound that
  permits this: `origin == "https://"+r.Host || origin == "http://"+r.Host`
  is unconditional at that line, so a bad value **cannot** lock out a
  same-origin console.
- **D8** Notification `cooldown_seconds` and `routes` **stay in
  `strategy.Params`**. They are already hot-swapped
  (`components.go:109-111`), already validated, already versioned. Moving
  them buys nothing and churns the strategy version that every opportunity
  cites as provenance. Cost accepted: the console's Notifications page
  writes to two endpoints (`/api/v1/config` for routing, `/api/v1/platform/
  settings` for enable/allowlist). The shared diff/confirm component
  (BL-02/03) already handles both documents.
- **D9** `SHADOW` is enumerated but reported `available: false`.
  `engine.go` branches only on `ModePaper`/`ModeRecord`;
  `simulation.NewShadow` exists but is never constructed, and
  `portfolio.ApplyCycle` returns `ErrShadowResult` (`portfolio.go:68-74`).
  Wiring it means changing how paper cycle results reach the portfolio —
  money-adjacent code this design is forbidden to touch. Enumerated with a
  reason beats silently dropped or silently broken.
- **D10** `ai.provider ∈ {anthropic, fake}`. There is **no OpenAI
  implementation** in `internal/ai` (only `anthropic.go` and `fake.go`).
  The `Advisor` interface (`ai.go:122-127`: `Name/Model/Analyze`) is
  provider-agnostic and would accept one with **no interface change**, so
  `openai` appears in `capabilities.ai_providers` as
  `available:false, reason:"provider not built"` — never in the enum a
  validator accepts.
- **D11** Secrets are **write-only** through the API. Never returned, never
  logged, never partially displayed — stronger than SKILL §42's "never
  display complete API secrets after entry", because no part is ever shown.
- **D12** Venue *display* (`platform.VenueTable()`) is separate from venue
  *enforcement* (`platform.CompiledVenues`, `settings.go:27-29`). The table
  may list OKX/Bybit/Bitget/Gate/MEXC honestly as unbuilt; the validator
  still refuses to enable anything not compiled in.

## 2. Operating mode (`platform.mode`) — T-059

### 2.1 Types

```go
// internal/platform/settings.go
type Settings struct {
    Platform PlatformSettings         `json:"platform"`
    Venues   map[string]VenueSettings `json:"venues"`
    Paper    PaperSettings            `json:"paper"`
    Telegram TelegramSettings         `json:"telegram"`
    AI       AISettings               `json:"ai"`
}

type PlatformSettings struct {
    Mode          config.Mode `json:"mode"`           // restart
    LogLevel      string      `json:"log_level"`      // hot: debug|info|warn|error
    AllowedOrigin string      `json:"allowed_origin"` // hot
}

// ModeTable is display and enforcement in one place — the same split
// §5 uses for venues. The enum the console renders is exactly this list;
// `available` is what Validate enforces. LIVE is absent by construction
// and has no config.Mode constant to be absent from.
func ModeTable() []ModeProfile // {ID, Available, Reason}
func Settable(m config.Mode) bool // == ModeTable entry with Available
```

### 2.2 Validation

`ModeTable()`:

| Mode | Available | Reason when not |
|---|---|---|
| `MARKET_DATA`, `RECORD`, `PAPER` | yes | — |
| `SHADOW` | **no** | `shadow execution is not wired into Engine.Run; enabling it changes the paper→portfolio result path (separate task)` (D9) |

`Validate` rejects anything `!Settable`, quoting the reason. Two values get
a named error rather than the generic one:

- `LIVE` / `live` → `mode LIVE is not an operating mode: live trading is
  permanently disabled (LiveExecutor returns ErrLiveTradingDisabled)`
- `REPLAY` / `BACKTEST` → `mode %s is a batch run, not a process mode:
  start it from Replays or Campaigns`

`ValidatePaperMode` folds **into** `Validate`, now reading
`s.Platform.Mode` instead of the injected process mode — `Validate` becomes
total again. `Service.Mode` (`service.go:77`) survives only as the v1 seed
and the `WithDefaults` fallback; its doc comment says so.

`Seed`/`WithDefaults` map **every non-settable `ARB_MODE`** — `REPLAY`,
`BACKTEST` **and `SHADOW`** — to `MARKET_DATA` in the document, with a log
line naming the substitution. `WithDefaults` goes one step further: the
env mode cannot see the stored `venues`/`paper` sections' cross-field
rules, so when the seeded mode makes the upgraded document invalid but
`MARKET_DATA` does not (a pre-expansion row with `paper_enabled=false`
under `ARB_MODE=PAPER`), it falls back to `MARKET_DATA` with the same
named WARN; a stored (non-empty) mode is never rewritten. Invalid
`ARB_LOG_LEVEL`/`ARB_ALLOWED_ORIGIN` seeds are substituted with the same
kind of named WARN (`SeedNotes`), never silently. `config.go:25` makes `SHADOW` a legal
`ARB_MODE` today and `.env.example:4` documents it, so a deployment running
`ARB_MODE=SHADOW` (or `cmd/replay`, which shares `buildPlatform`) would
otherwise seed a document its own validator rejects and refuse to boot —
the exact D3 failure. The rule is written once, as `Settable(m)`, so it
cannot drift from `ModeTable()`.

### 2.3 Engine and API seams

| Today | After |
|---|---|
| `engine.go:794` `e.cfg.Mode == config.ModePaper` | `e.currentMode() == config.ModePaper` |
| `engine.go:505` **and** `:800` `EnsurePaperSession(..., string(e.cfg.Mode), ...)` | both take `e.currentMode()` |
| `engine.go:902` ready-notification text, `:929` `ModeRecord` recorder start | `e.currentMode()` |
| `app/ai.go:15`, `app/reporting.go:20`, `app/telegram.go:35` | `e.currentMode()` |
| `components.go:333` wires `apiServer.Paper` **only if** `cfg.Mode == PAPER` | wired unconditionally — `paperProxy` is already nil-safe (`Running()` false, `Pause/Resume` no-ops) and gating at build time would 404 the paper routes after a switch into PAPER until a redeploy |
| `components.go:346` `"mode": string(cfg.Mode)` in the health snapshot | `"mode": {"running": engine.Mode(), "configured": platformSvc.Current().Settings.Platform.Mode}` |
| `api/server.go:188` `string(s.cfg.Mode)` | `s.Mode()` (injected `func() string`; falls back to `cfg.Mode` when nil, i.e. the API profile reports the configured mode) |

`Engine.Mode()` reads `e.settings.Platform.Mode` under `e.mu`, falling back
to `platform.Seed(e.cfg)` exactly as `Engine.settingsOrSeed` already does
(`engine.go:345-353`). `Run` snapshots it once at entry, so a mode never
changes mid-run.

**The console mode banner must show the RUNNING mode**, not the configured
one (`web/src/components/ConsoleShell.tsx:75-88` reads a single value
today). After a mode change it renders `PAPER` with an amber annotation
*"configured: RECORD — restart pending"*.

### 2.4 Guard rails

- Changing `platform.mode` is restart-scoped, so the Supervisor's
  `Settings.Subscribe` callback raises the banner and appends a **specific**
  reason: `"platform settings v9: mode MARKET_DATA→PAPER"` (generic
  `"platform settings v9"` for other restart-scoped fields).
- **Leaving RECORD with a live recorder** is already refused by the T-057
  §2.5 `recording_active` guard unless the request carries
  `stop_recording`. Kept as-is: the operator ticks the checkbox the backend
  asked for; the Supervisor then calls `Recorder().Stop()` *before*
  cancelling (T-057 §2.4 step 2), so the last segment closes cleanly and
  the stopped session id lands in the audit row.
- **Entering PAPER** requires `paper_enabled` on every enabled venue and a
  `paper.balances` key set matching the starting assets — both already in
  `Validate`, so the failure lands at **apply** time, never at restart
  (T-057 D8). `Engine.Run`'s existing `EnsurePaperSession` call then mints
  the session for free.
- **Leaving PAPER** closes the outgoing session via the Supervisor's
  existing `EndPaperSession` (T-057 E10). Persisted cycles/opportunities
  are untouched; in-memory balances reset — the confirm dialog says so.

## 3. Secrets vault — T-060

### 3.1 Storage

`migrations/000008_secrets.{up,down}.sql` (000007 is orders/fills/risk/
replay):

```sql
CREATE TABLE secrets (
    name       TEXT PRIMARY KEY,
    ciphertext BYTEA NOT NULL,
    nonce      BYTEA NOT NULL,
    key_id     TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT REFERENCES users(id)
);
```

One row per name, overwritten on write. **No version history**: a table of
historical secret ciphertexts is a larger blast radius for zero operational
value — the audit trail records *that* a write happened, by whom, when.
Down drops the table.

### 3.2 Crypto — `internal/secrets`

AES-256-GCM. Key from `ARB_SECRET_KEY`: standard-base64, decoding to
**exactly 32 bytes**; anything else is an error naming the actual length.

`ARB_SECRET_KEY` is read by `secrets.KeyFromEnv()` **inside
`internal/secrets`** and deliberately does **not** enter
`config.Bootstrap`. Reason: `config.go:141-142` states that adding a secret
field to `Bootstrap` requires masking it in `Redacted()`, enforced by the
config test — so putting it there means a T-060 change lands with a red
test in an unrelated package for no benefit. Keeping the master key in the
one package that uses it means it cannot be logged by a future `Redacted()`
omission at all. `internal/config` is untouched by T-060.

- `nonce` is 12 random bytes per write (`crypto/rand`), never reused.
- **AAD = the secret name.** Binds ciphertext to its row: a swapped row
  fails authentication instead of decrypting as a different secret.
- `key_id = hex(sha256(key))[:8]`, stored per row. A row whose `key_id` does
  not match the process key is reported
  `present:true, readable:false, reason:"encrypted under key ab12cd34; this process holds ef56ab78"` —
  an honest, actionable failure instead of an opaque auth error.
- **Boot policy:** `ARB_SECRET_KEY` unset → the vault does not open
  (`ErrNoKey`), logged at WARN as *"secrets vault disabled: ARB_SECRET_KEY
  unset; env-provided secrets still work"*. **The platform still runs** on
  env-provided secrets. Vault write routes return `503 vault_unavailable`;
  `GET /api/v1/secrets` returns `vault_configured:false`.

### 3.3 Registry and resolution

```go
type Spec struct{ Env, Label string; MinLen int }

var Known = map[string]Spec{
    "anthropic_api_key":  {Env: "ANTHROPIC_API_KEY",   Label: "Anthropic API key",  MinLen: 20},
    "telegram_bot_token": {Env: "ARB_TELEGRAM_TOKEN", Label: "Telegram bot token", MinLen: 20},
}

type SecretSource interface{ Get(ctx context.Context, name string) (string, string, bool) } // value, source, ok
```

Implementations: `Vault` (DB), `Env`, and `Chain{Vault, Env}` — **vault
first, env fallback**, `source` reporting which won. A `PUT` to a name
outside `Known` is `404 unknown_secret`. This closed registry is what keeps
the vault from becoming a general store for exchange trading keys.

Value validation: length ≥ `Spec.MinLen`, ≤ 4096, **printable ASCII only**
(a token containing `\n` is an HTTP header-injection vector — the Telegram
client builds its base URL from it and the Anthropic client puts it in
`x-api-key`). Surrounding whitespace is **trimmed, not rejected**: pasting a
token with a trailing newline is the single most common operator error and
trimming is unambiguous. Interior whitespace is rejected.

Consumers resolve at (re)start:
`buildAdvisor(cfg, log, src)` calls `src.Get(ctx, "anthropic_api_key")`
instead of reading `cfg.AnthropicAPIKey` — and is re-invoked by the
`ai.Switch` on every `ai.*` settings swap, so an AI key written to the vault
applies **immediately**. The Telegram token is read once at
`components.go:229`; a token written later applies on the next **process**
restart (D5). `PUT` responses carry the backend-computed
`"applies": "immediately" | "process_restart"` — the same
never-hardcode-it-on-the-frontend rule as `field_timing`.

Rotation: overwrite via `PUT` (idempotent), or `DELETE` to fall back to env.
Rotating `ARB_SECRET_KEY` itself is a documented manual procedure —
`DELETE` every row, restart with the new key, `PUT` every value — not an
automated re-encryption path; with two write-only secrets, automation would
be more code than the procedure it replaces.

### 3.4 Routes

| Method | Path | Permission | CSRF |
|---|---|---|---|
| GET | `/api/v1/secrets` | `view:system` | — |
| PUT | `/api/v1/secrets/{name}` | `system:config` | yes |
| DELETE | `/api/v1/secrets/{name}` | `system:config` | yes |

```jsonc
// GET  → {"vault_configured": true, "key_id": "ab12cd34", "secrets": [
//   {"name":"anthropic_api_key","label":"Anthropic API key","present":true,
//    "source":"vault","readable":true,"updated_at":"…","updated_by":"u_1",
//    "applies":"immediately"},
//   {"name":"telegram_bot_token","label":"Telegram bot token","present":true,
//    "source":"env","readable":true,"applies":"process_restart"}]}
// PUT  {"value":"…"} → 200 {"name":…,"present":true,"source":"vault",
//                            "updated_at":…,"updated_by":…,"applies":…}
```

Never a value, never a prefix, never a last-4. Request body read through an
8 KiB `io.LimitReader`; the handler zeroes its buffer after use. Audit rows:
`secret.write` / `secret.delete`, `entity=secret`, `entity_id=name`,
`after={"name":…,"present":…,"key_id":…}` — no `before`, because there is
nothing safe to record. Errors: `400 invalid_secret` (length/charset),
`404 unknown_secret`, `503 vault_unavailable`.

## 4. Providers, logging, origin — T-059

### 4.1 AI advisor

```go
type AISettings struct {
    Enabled  bool       `json:"enabled"`
    Provider string     `json:"provider"` // anthropic | fake
    Model    string     `json:"model"`
    Schedule AISchedule `json:"schedule"`
    Budget   AIBudget   `json:"budget"`
}
type AISchedule struct { // 0 disables that cadence
    HourlyMinutes int `json:"hourly_minutes"` // 0 or 15..1440
    DailyHours    int `json:"daily_hours"`    // 0 or 1..168
    WeeklyHours   int `json:"weekly_hours"`   // 0 or 24..720
}
type AIBudget struct {
    MaxAnalysesPerDay int `json:"max_analyses_per_day"` // 1..96
    MaxOutputTokens   int `json:"max_output_tokens"`    // 256..8192
}
```

Validation: `provider ∈ {anthropic, fake}` (D10); `model` non-empty, ≤64
chars, `[A-Za-z0-9._-]` only; ranges above; `enabled && provider=="anthropic"
&& no resolvable key` is **accepted with a warning in the apply response**,
not rejected — the operator may legitimately save settings before writing
the key, and the status endpoint reports `enabled:true, running:false,
reason:"no anthropic_api_key"`.

Wiring (D4): `ai.Switch` implements `Advisor` over an
`atomic.Pointer[Advisor]`; `Set(nil)` makes `Analyze` return
`ErrAdvisorDisabled`. `ai.Service.Advisor` is the switch, assigned once.
`platformSvc.Subscribe` rebuilds through `buildAdvisor(cfg, log, secrets)`
and calls `Set`. `RunAnalysis` returns `ErrAdvisorDisabled` **early, without
incrementing failures or raising a WARNING** — disabled is not a failure,
and the old code would have produced an hourly alert loop.

`ai.Scheduler` replaces its three fixed tickers (`service.go:416-421`) with
a re-armable timer loop that reads a `Cadence func() AISchedule` at each
fire, so cadence changes apply on the next tick without a restart. A zero
cadence disarms that timer.

Budget: `MaxAnalysesPerDay` is a **per-process UTC-day counter** in
`ai.Service` (reset on day rollover; **not persisted** — a process restart
resets it, and the console tooltip says so). Exceeding it logs and skips at
INFO. `MaxOutputTokens` replaces the hardcoded `"max_tokens": 2048` at
`anthropic.go:37`, which becomes a field on `Anthropic`.

Permission: `ai` → `system:config` (D2 default). The AI advisor still gets
**no route into any settings document**: recommendations remain
`strategy.Params`-only and human-approved (`ai/service.go:255`).

### 4.2 Telegram

`TelegramSettings` gains `Disabled bool` — **negative on purpose** (D3): a
persisted `{"telegram":{"allowlist":[…]}}` unmarshals with the zero value,
which must mean "keep delivering". An `Enabled bool` would silently mute
both bot commands and alert pushes on a document nobody edited, on a
channel T-057 §1.3 classifies as a security control. `telegram.disabled` is
the name on the **wire too** — in the diff, in `field_timing`, in the apply
body. The API does not invert it into an `enabled` alias: one field with two
names is how a diff row and a form control drift apart. The console renders
a toggle labelled *"Telegram notifications"* whose ON position submits
`disabled: false`, and the confirm dialog shows the raw path. `allowSet` (`components.go:557`) gains a
`disabled` flag: when set, `Allowed()` returns false and `IDs()` returns
nil, so **commands and pushes go quiet together** — the exact invariant the
existing comment protects. The bot is
still constructed only when a token resolves at boot **and** the allowlist
is non-empty; `TelegramStatusView` gains `Reason string` ("disabled in
settings" / "no token configured" / "allowlist empty at boot").

### 4.3 Log level and allowed origin

`app.NewLogger` keeps its signature and installs a package-scoped
`*slog.LevelVar`; `app.SetLogLevel(string) error` and
`app.CurrentLogLevel() string` are the seam a `platformSvc.Subscribe`
callback calls. Justification for the package-scoped var: the process has
exactly one logger, and threading a `LevelVar` through six `cmd/` entry
points and `BuildComponents` to reach one callback is more plumbing than the
thing it configures. `slog.LevelVar` is atomic, so this is race-free.

**Console warning, mandatory copy:** *"debug logs every rejected
opportunity"* — `engine.go:1132` emits a record per rejection in
`consumeEvents`, and a real fixture produced 4988 rejections against 12
qualified in one period (MASTER_PLAN, BL-31). It is off the book→recalc hot
path but on the consumer, so it is a measurable cost, not free.

`allowed_origin` (D7): validated as a strict absolute origin —
`scheme://host[:port]`, scheme ∈ {http, https}, no path/query/fragment, no
`*`, ≤255 chars. `api.Server` reads it through an atomic accessor at
`ws.go:26`.

## 5. Venues and capabilities — T-061

```go
type VenueProfile struct {
    ID, Name  string                 `json:"id","name"`
    Available bool                   `json:"available"`
    Reason    string                 `json:"reason,omitempty"`
    Discount  *CompiledVenueDiscount `json:"discount,omitempty"`
}

func VenueTable() []VenueProfile // superset of today's CompiledVenueTable
```

Entries: `binance` (available), `okx` (`"connector not built (T-050)"`),
`bybit`/`bitget`/`gate` (`"connector not built (T-051)"`), `mexc`
(`"connector not built (T-051; researched T-056)"`). `CompiledVenues`
(`settings.go:27`) stays the enforcement gate — enabling an unavailable
venue is `400 connector_unavailable` naming the blocking task (D12).

`GET /api/v1/platform/capabilities` (`view:system`) serves `ModeTable()`,
`VenueTable()` and the provider table — three lists, one shape, one source
each →
`{"modes":[{"id":"PAPER","available":true},…],"venues":[…],"ai_providers":
[{"id":"anthropic","available":true},{"id":"fake","available":true},
{"id":"openai","available":false,"reason":"provider not built"}]}`.
`GET /api/v1/platform/venues` stays as a thin alias over the same
`VenueTable()` (shipped console keeps working; fields are additive only) —
one source function, two routes, no divergence. Every venue card renders
*"Public market data only — no API keys are used or accepted."*

## 6. Wire contract

`field_timing` stays a **two-valued** map (`"hot"` | `"restart"`), extended:

| Path | Timing | Permission |
|---|---|---|
| `platform.mode` | restart | `system:config` |
| `platform.log_level`, `platform.allowed_origin` | hot | `system:config` |
| `ai.*` | hot | `system:config` |
| `telegram.disabled` | hot | `system:config` |
| `telegram.allowlist` | hot / restart (existing `botRunning` caveat) | `system:config` |
| `venues.*` | restart | `exchange:config` |
| `paper.*` | restart | `system:config` |

`hotPrefixes` (`settings.go:394`) becomes
`{"telegram.", "ai.", "platform.log_level", "platform.allowed_origin"}` —
i.e. restart-scoped is exactly `venues.*`, `paper.*`, `platform.mode`.

Console (Settings page, five sections):

- **Operating mode** — select of the four modes, SHADOW disabled with its
  reason inline; a permanent line beneath: *"LIVE is not an option: this
  platform never places real orders."* The confirm dialog adds a
  mode-specific consequence line (new recording session / new paper session
  with configured starting balances, persisted history kept / no simulation
  runs but opportunities are still detected) and, when leaving RECORD, the
  *"Stop the active recording ({id}, running 41m)"* checkbox the backend
  asked for.
- **AI advisor** — enable toggle, provider select (openai listed disabled
  with its reason), model, three cadence inputs, two budget inputs, and a
  status line `enabled / running / last analysis / analyses today N of M`.
- **Notifications** — Telegram enable + allowlist chips (this document),
  severity routes + cooldown (strategy document, D8), and a token status row
  linking to Security.
- **Security** — one row per registry entry: `Present` / `Not set`, source
  (`vault` / `environment`), updated at/by, `Set…` and `Remove` actions, and
  the honest `applies` string. The input is `type=password`,
  `autocomplete=off`, cleared on submit; nothing is ever rendered back.
- **Venues** — every known venue with an availability badge; unavailable
  ones are non-interactive with the reason as the tooltip.

## 7. Tests

`internal/platform`: `Validate` per §2.2 (`"live"`, `"LIVE"`, `REPLAY`,
`BACKTEST`, `SHADOW`, garbage); every `AISettings` bound; origin/log-level
validators; **`WithDefaults` fed a raw JSON byte literal that omits
`platform` and `ai` entirely** (a round-tripped struct carries the keys with
zero values and would not exercise the branch that matters) asserting load
*and* `Get` *and* rollback all normalize; **a raw literal with a `telegram`
section carrying only `allowlist`, asserting delivery stays on** (D3's
quiet-failure case); `Seed`/`WithDefaults` map `ARB_MODE` ∈
{`REPLAY`, `BACKTEST`, `SHADOW`} → `MARKET_DATA` — one row each, since
`SHADOW` is the one a real deployment may actually be running; a
reflect-over-`Settings`-json-tags test asserting every top-level section
has an explicit `PermissionForSection` case; `ModeTable()`/`Settable`
agree with `Validate` for every `config.Mode` constant (a table-driven loop,
so adding a mode constant without a table entry fails the build's tests).

`internal/secrets`: round-trip; wrong key → auth failure, not garbage; AAD
mismatch (row swapped between names) → failure; `key_id` mismatch reported
rather than returned; nonce uniqueness over 1000 writes; base64/length
errors on `ARB_SECRET_KEY`; `Chain` precedence and `source` reporting;
trim-vs-reject whitespace cases.

`internal/api`: RBAC denial for VIEWER/OPERATOR on `PUT`/`DELETE
/secrets/*`; CSRF rejection; `unknown_secret`; `vault_unavailable`; **a
log-capture assertion that the submitted value appears in no emitted record
for `PUT /api/v1/secrets/{name}`** (the request-logging middleware is not
assumed body-safe); `capabilities` shape; `field_timing` contains the new
paths with the right values.

`internal/ai`: `Switch` nil → `ErrAdvisorDisabled` with failures counter and
notifier untouched; hot provider swap observed by the next `RunAnalysis`;
scheduler cadence change takes effect without restart and a zero cadence
disarms; daily cap skips and resets on UTC rollover; `MaxOutputTokens`
reaches the request body (httptest server asserts it).

`internal/app`: mode read from settings, not `cfg` (run once in
MARKET_DATA, apply PAPER, restart, assert a paper session exists and
`Status()` reports PAPER); `SetLogLevel` changes emitted records at runtime;
`allowSet` disabled → both `Allowed` and `IDs` go quiet, and the zero-value
`TelegramSettings` leaves both live; supervisor
`PendingReasons` names the mode transition. All with `-race`.

## 8. Phasing and effort

| Task | Scope | Effort |
|---|---|---|
| **T-059** | §2 mode (types, validation, `WithDefaults`, engine/API seams, guard rails), §4 AI switch + scheduler + budget, Telegram enable, log level, allowed origin, `field_timing` | ~3 days, 1 engineer. Order: settings shape + `WithDefaults` + tests → engine/API mode seams → `ai.Switch` + scheduler → telegram/log/origin → API contract. `WithDefaults` lands **first**: without it the deploy fails to boot. |
| **T-060** | §3 vault: `internal/secrets`, migration 000008, pgx store, routes, `SecretSource` consumers | ~2 days, 1 engineer. Parallelizable with T-059; the only shared file is `components.go`'s `buildAdvisor` signature. |
| **T-061** | §5 `VenueTable`, `capabilities` route, validator reason strings; console work owned by the frontend agent | ~0.5 day backend |

No migration for T-059 (the document is JSONB; new sections are additive —
which is exactly why D3 exists). Migration **000008** for T-060 only.

## 9. Non-goals

Live trading, in any form, under any flag. Exchange trading API keys,
read-only or otherwise. Automatic `ARB_SECRET_KEY` re-encryption. Secret
version history. Per-secret RBAC finer than ADMIN. Scheduled or automatic
mode switches. Wiring SHADOW execution (D9 — money-adjacent, separate
task). Moving notification routing out of `strategy.Params` (D8). An OpenAI
provider (D10 — the seam is ready; the implementation is a separate task).
`ARB_DATABASE_URL`, `ARB_HTTP_ADDR`, `ARB_METRICS_ADDR`, `ARB_RECORDING_DIR`
(changing it orphans `market_recording_metadata.segment_files`),
`ARB_ADMIN_EMAIL`/`ARB_ADMIN_PASSWORD` (a process needs one credential
before a UI can create any), `ARB_SEED` and `ARB_SHUTDOWN_GRACE` stay
env-only and are documented as such in `.env.example`.
