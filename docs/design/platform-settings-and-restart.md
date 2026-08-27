# Platform Settings and Supervised Engine Restart

Console package **C**. Closes `docs/design/console-ux-audit.md` **BL-13b**
(editable symbols/starting assets) and **BL-15** (venues & fees), which the
audit correctly refused to build against a missing backend. Tracked as
**T-057** in `docs/MASTER_PLAN.md`.

Scope: a DB-backed, versioned **platform settings** document; a **supervised
engine restart** that applies the restart-scoped half of it; the routes,
permissions, audit entries and frontend contract for both. Two engineers,
~3 days: engineer **A** takes §1 + §3 (`internal/platform`, storage, API),
engineer **B** takes §2 (engine re-entrancy + supervisor + wiring). The
frontend (§4) is a frozen contract, built as a follow-up under BL-12.

## 0. Decisions

- **D1** Platform settings live in a **new** `internal/platform` service and
  `platform_settings` table, *not* in `strategy.Params` (§1.1).
- **D2** The versioned document is authoritative for symbols, starting
  assets, venue enablement and fees. `markets.enabled` / `exchanges.enabled`
  stay **metadata mirrors**, never a second writable copy (§1.4).
- **D3** Every settings field is **restart-scoped** except the Telegram
  allowlist, which is hot (§1.3). Fees are restart-scoped on purpose.
- **D4** Restart = one **stable** `*app.Engine` whose `Run` is re-entered by
  a supervisor. No engine value is ever replaced, so every existing seam
  (`recorderProxy`, `paperProxy`, `ScannerStatus`, `Reads`, `engine.Hub`,
  `ai.Scheduler.InputFor`, `telegramServices`) keeps working unchanged (§2).
- **D5** Env vars remain **first-boot seeds only**. After version 1 exists,
  `ARB_SYMBOLS`/`ARB_STARTING_ASSETS`/`ARB_PAPER_BALANCE`/
  `ARB_TELEGRAM_ALLOWLIST` are ignored and logged as ignored.
- **D6** `internal/platform` reuses `strategy.DiffAny` / `TopLevelSections`
  for diffs and section→permission mapping. It does **not** reuse
  `strategy.Service`, and `strategy.Service` is not refactored into a
  generic — a working, shipped service is not touched without measured need.
- **D7** No exchange API keys are accepted, stored, or displayed anywhere in
  this feature. Live trading is untouched: `LiveExecutor` still returns
  `ErrLiveTradingDisabled`.
- **D8** A settings version that cannot build a topology is rejected at
  apply time (dry-run `graph.Build`), so the restart path is never the place
  where a bad symbol list is discovered.

## 1. Platform settings

### 1.1 Why not `strategy.Params`

Three independent reasons, strongest first:

1. **RBAC fails open.** `internal/auth/rbac.go:72-77`
   `PermissionForConfigSection` is `section == "risk" → PermRiskConfig, else
   → PermScannerConfig`. OPERATOR holds `PermScannerConfig`. Adding a
   `venues` or `fees` section to `Params` would **silently grant every
   OPERATOR the ability to change fee tiers and venue enablement**, which
   the matrix reserves for ADMIN (`PermExchangeConfig`, `PermSystemConfig`).
2. **Provenance churn.** `opportunities.config_version` and
   `paper_sessions.config_version` cite the strategy version, and replay /
   campaign comparisons diff those versions. Bumping the strategy version
   because an operator added a Telegram user pollutes the series that
   `docs/design/console-ux-audit.md` §1.12 compares.
3. **Validation shape.** `Params.Validate()` is pure and total — that is why
   it can be mirrored client-side (audit §4.4). Symbol validation needs the
   venue's `exchangeInfo` and a topology dry-run; it is not pure, and it
   returns a *plan* (triangle count), not just an error.

Cost of the split: ~110 lines of near-duplicate store plumbing (§1.4). Paid
knowingly; a shared generic version-store is a refactor of shipped code
with no measured need.

### 1.2 Types — `internal/platform/settings.go`

```go
package platform

// Settings is the complete platform-scope document. bps values are
// decimal (never float64); paper balances are decimal strings end to end.
type Settings struct {
    Venues   map[string]VenueSettings `json:"venues"`   // key: exchange id, e.g. "binance"
    Paper    PaperSettings            `json:"paper"`
    Telegram TelegramSettings         `json:"telegram"`
}

type VenueSettings struct {
    Enabled        bool        `json:"enabled"`
    PaperEnabled   bool        `json:"paper_enabled"`
    Symbols        []string    `json:"symbols"`         // sorted, upper-case
    StartingAssets []string    `json:"starting_assets"` // sorted, upper-case
    Fees           FeeSettings `json:"fees"`
}

type FeeSettings struct {
    MakerBps      decimal.Decimal        `json:"maker_bps"`
    TakerBps      decimal.Decimal        `json:"taker_bps"`
    Overrides     map[string]FeeOverride `json:"overrides,omitempty"` // key: symbol
    TokenDiscount bool                   `json:"token_discount"`
}

type FeeOverride struct {
    MakerBps decimal.Decimal `json:"maker_bps"`
    TakerBps decimal.Decimal `json:"taker_bps"`
}

type PaperSettings struct {
    Balances map[string]string `json:"balances"` // asset → decimal string
}

type TelegramSettings struct {
    Allowlist []int64 `json:"allowlist"` // token stays env-only
}

func (s Settings) Clone() Settings           // deep copy; maps/slices are the reference types
func (s Settings) Validate() error           // pure, bounded, no I/O
func Seed(b config.Bootstrap) Settings       // first-boot document from env
func (s Settings) RestartScoped(diff map[string]strategy.Change) bool
```

`TokenDiscount` is a **toggle only**. The discount rate, pay asset and
API eligibility come from a per-venue constant table in `internal/fees`
(sourced from `docs/research/fees.md`) — an operator can say "I pay fees in
BNB", never "the discount is 40%". For a venue whose discount excludes API
trades (Bybit MNT), the toggle validates as an error, not a silent no-op.

### 1.3 Field table

| Path | Timing | Validation | Permission |
|---|---|---|---|
| `venues.{ex}.enabled` | restart | ≥1 venue enabled; venue id must be a compiled-in connector | `exchange:config` |
| `venues.{ex}.paper_enabled` | restart | in `PAPER` mode the enabled venue must have it set | `exchange:config` |
| `venues.{ex}.symbols` | restart | 3..60, upper-case, unique; each exists in `exchangeInfo` with status `TRADING`; dry-run topology ≥1 triangle | `exchange:config` |
| `venues.{ex}.starting_assets` | restart | 1..8; each is base or quote of ≥1 selected symbol; each starts ≥1 triangle | `exchange:config` |
| `venues.{ex}.fees.maker_bps`, `.taker_bps` | restart | decimal in **(0, 100]** — `fees.NewSchedule` rejects non-positive defaults (`fees.go:56`) | `exchange:config` |
| `venues.{ex}.fees.overrides.{symbol}` | restart | symbol ∈ `symbols`; each leg in **[0, 100]**; zero allowed (verified promo pairs only, per `fees.SetOverride`) | `exchange:config` |
| `venues.{ex}.fees.token_discount` | restart | venue's discount must apply to API-executed trades | `exchange:config` |
| `paper.balances.{asset}` | restart | decimal string, `> 0`, `≤ 1e9`; key set == union of all enabled venues' starting assets | `system:config` |
| `telegram.allowlist` | **hot** | ≤32 entries, each `> 0`, unique | `system:config` |

Fees are deliberately restart-scoped. The live schedule is read on the hot
path by the scanner (`engine.go:350`); mutating it in place would be a data
race, and swapping it mid-session makes a paper session's cycles
incomparable with each other. A fee change starts a clean session.

The allowlist is hot because it is a *security* control — revoking a
Telegram user must not wait for a restart. Honest caveat surfaced in the
UI: if the process booted with an **empty** allowlist the bot was never
constructed (`components.go:170`), so adding the *first* entry needs a
restart. The field's UI tag is computed, not static: `hot` when the bot is
running, `on restart` when it is not.

### 1.4 Storage, service, seeding

`migrations/000006_platform_settings.up.sql` (000005 is campaign verdicts,
landing concurrently — the number is ordering, not coupling):

```sql
CREATE TABLE platform_settings (
    version        BIGSERIAL PRIMARY KEY,
    created_by     TEXT REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    active         BOOLEAN NOT NULL DEFAULT FALSE,
    payload        JSONB NOT NULL,
    diff           JSONB,
    parent_version BIGINT REFERENCES platform_settings(version)
);
CREATE UNIQUE INDEX platform_settings_active_idx ON platform_settings(active) WHERE active;
```

Same shape as `strategy_configs` (append-only, exactly one active, diff and
actor recorded, rollback = new version). No DDL changes to `markets`,
`exchanges` or `triangles`: `storage.UpsertMarkets` writes
`enabled = EXCLUDED.enabled` on every metadata sync
(`internal/storage/markets.go:40`), so any operator flag stored there is
erased on the next bootstrap. The document is the source of truth; the
Exchanges page may *project* it onto those rows for display, never read
back from them.

- `internal/platform/service.go` — `Service` with `Load/Current/Apply/
  ApplyAuthorized/Rollback/RollbackAuthorized/Get/List/Subscribe/PlanDiff`,
  mirroring `strategy.Service` including the **in-lock** `Authorize` gate
  (the TOCTOU fix documented at `strategy/service.go:150-155`).
- `internal/storage/platformsettings.go` — pgx store; `platform.NewMemoryStore()`
  for DB-less dev and tests.
- `internal/strategy/diff.go` — widen `flatten(p Params)` to `flatten(v any)`
  and export `DiffAny(oldV, newV any) (map[string]Change, error)`;
  `Diff(oldP, newP Params)` becomes a one-line wrapper. `TopLevelSections`
  is unchanged and drives the section→permission gate.
- `buildPlatform(log, store, cfg)` in `internal/app/components.go`, next to
  `buildStrategy` (`components.go:407`): `Load` seeds version 1 from
  `platform.Seed(cfg)` when the table is empty, and logs
  `"env symbol/asset/balance/allowlist variables ignored; platform settings v%d is authoritative"` when it is not.

### 1.5 Consumption seams

| Today | After |
|---|---|
| `engine.go:234-248` scopes markets by `e.cfg.Symbols` | scopes by `settings.Venues[ex].Symbols` |
| `engine.go:253-256` starts from `e.cfg.StartingAssets` | from `settings…StartingAssets` |
| `engine.go:307-311` hardcodes 10/10 bps | `fees.NewSchedule(ex, conv, Rate{Maker: makerBps/10000, Taker: takerBps/10000})`, then `SetOverride` per entry, then `sched.Discount = fees.VenueDiscount(ex)` gated on `TokenDiscount` |
| `engine.go:313-321` one `PaperBalance` for every asset | per-asset map from `settings.Paper.Balances` |
| `components.go:170-196` two copies of the allowlist (`telegram.Bot{Allowlist}` **and** `telegram.PushSink{ChatIDs}`) | both read one `*platform.AllowSet` (`atomic.Pointer[map[int64]bool]`) updated by `Subscribe`. Updating only the Bot would leave a revoked user still receiving pushes |
| `config.go:58-60,92-94,120-131` | unchanged loaders, comment marked "first-boot seed only (D5)" |

Validation that needs market metadata uses a catalog seam:

```go
type Catalog interface {
    Markets(ctx context.Context, ex exchange.ExchangeID) ([]exchange.Market, error)
}
// ValidateAgainstCatalog returns the plan a settings document would produce.
func ValidateAgainstCatalog(ctx, s Settings, c Catalog) (Plan, error)

type Plan struct {
    Venue               string `json:"venue"`
    Markets             int    `json:"markets"`
    Triangles           int    `json:"triangles"`
    RejectedUntradeable int    `json:"rejected_untradeable"`
    StartingAssets      []string `json:"starting_assets"`
}
```

Implementations: `engineCatalog` (the engine retains the full
`bootstrapMetadata` slice, see E2 below) preferred; `storeCatalog` over a
new `storage.ListMarkets` as the API-profile fallback. `Plan` is computed
with the real `graph.Build`, so the preview's triangle count is the number
the engine will actually get (D8).

## 2. Supervised restart

### 2.1 The invariant that makes this safe

> **Nothing registered on the Hub, the Metrics meter or the Notifier may
> capture a per-run local. It resolves through an `e.mu`-guarded accessor.**

`Engine.Run` today registers callbacks on process-lifetime objects while
closing over values it rebuilds every run. Re-entering `Run` without fixing
this leaves the console and Prometheus reporting a **dead** engine. This
single sentence is the review checklist for §2.2.

### 2.2 Engine changes (`internal/app/engine.go`)

- **E1** At `Run` entry, under `e.mu`: `scn, topo, pap, rctl, port, feed,
  resv, brk = nil`, `ready = false`, `recentOpps/rejectCounts` cleared —
  *before* `bootstrapMetadata`, which retries with backoff (`engine.go:604-620`)
  and would otherwise leave `Status()` advertising the dead topology as
  ready for minutes. `Status()` already nil-guards every field.
- **E2** Retain the full metadata slice: `e.catalog = markets` under `e.mu`,
  exposed as `Engine.Catalog()` for §1.5.
- **E3** **Child goroutines must not outlive `Run`.** `engine.go:497-506`
  spawns feed/scanner/consumer/paper/outbox into `errCh`, and
  `engine.go:512-514` returns on `ctx.Done()` without waiting — violating
  `Component`'s own contract (`app.go:22`) and, on restart, producing two
  feeds, two scanners and two outboxes. Replace with a `sync.WaitGroup`;
  on `ctx.Done()` wait up to `cfg.ShutdownGrace` before returning. This is
  also what makes `Outbox.Run`'s cancel-path drain (`outbox.go:74-78`,
  3s deadline) actually complete before the next run starts — the
  "never lose persisted history" guarantee.
- **E4** `wireMetrics` runs under a `sync.Once`, and every closure in
  `metrics.EngineSources` reads `e.scanner()`, `e.topology()`, `e.feed()`…
  accessors (nil → zero values) instead of the locals it captures today
  (`engine.go:625-734`). Re-registering OTel instruments per restart would
  double-count.
- **E5** `Hub.RegisterTopic("recordings", …)` (`engine.go:487-489`) closes
  over the `rctl` **local** → use `e.Recorder()`. `"scanner"` already goes
  through `e.Status()`. `RegisterTopic` overwrites (`realtime/hub.go:69-73`),
  so re-registration is safe.
- **E6** `e.Strategy.Subscribe` (`engine.go:373`) currently **appends a
  callback per run**: `strategy.Service.onSwap` grows unboundedly and stale
  callbacks call `SetStrategy` on dead scanners. Register once under a
  `sync.Once`; the callback resolves the current scanner under `e.mu` and
  no-ops when nil.
- **E7** `ApplySettings(s platform.Settings, version int64)` stores the
  document the next `Run` will read. Called by the supervisor **between**
  runs only, so `Run` reads it without contention.
- **E8** Each run mints a new `sessionID` → a new `paper_sessions` row.
  Persisted `paper_cycles`/`opportunities` from prior sessions are never
  touched. In-memory reservation and portfolio balances **do** reset to the
  configured starting balances; §4 says so in the confirm dialog.
- **E9** `paperEng.Resume()` is unconditional today (`engine.go:463`). A
  restart must not silently un-pause a deliberately paused, RBAC-gated,
  audited control: the supervisor carries `Paper().Running()` across the
  boundary and re-pauses when it was paused before the restart.
- **E10** Nothing writes `paper_sessions.ended_at` today (`storage/records.go:181-185`
  only inserts). Restart makes "many sessions per process" normal for the
  first time, so an unbounded set of NULL-ended sessions would make "the
  current session" ambiguous for the Paper and PnL views. Add
  `Store.EndPaperSession(ctx, id, at)` and call it in §2.4 step 4.

### 2.3 Supervisor (`internal/app/supervisor.go`)

```go
type EngineRunner interface {
    Run(ctx context.Context) error
    ApplySettings(s platform.Settings, version int64)
}

type RestartState string // "ready" | "pending" | "restarting" | "failed"

type RestartStatus struct {
    State           RestartState `json:"state"`
    SettingsVersion int64        `json:"settings_version"`  // version the engine is running
    PendingVersion  int64        `json:"pending_version,omitempty"` // newer version with restart-scoped changes
    RequestedBy     string       `json:"requested_by,omitempty"`
    RequestedAt     *time.Time   `json:"requested_at,omitempty"`
    ReadyAt         *time.Time   `json:"ready_at,omitempty"`
    Restarts        int64        `json:"restarts"`
    PendingReasons  []string     `json:"pending_reasons,omitempty"` // human-readable, e.g. "platform settings v8", "strategy v13: scanner.workers"
    Error           string       `json:"error,omitempty"`
}

type RestartRequest struct {
    Actor, Reason string
    StopRecording bool
    Done          chan error // buffered(1); closed when the new run is ready or failed
}

type Supervisor struct {
    Engine    EngineRunner
    Settings  *platform.Service
    Recorder  func() *marketdata.RecorderControl
    Paper     func() *paper.Engine
    Campaigns interface{ Busy() (string, bool) } // new 4-line method on campaign.Runner over r.current
    Grace     time.Duration
    Log       *slog.Logger
    Notify    func(notification.Severity, string, string, string)
    OnState   func(RestartStatus) // hub publish on "health"
    Audit     func(actor, action, entity string)

    reqs chan RestartRequest // cap 1
    mu   sync.Mutex
    st   RestartStatus
}

func (s *Supervisor) Name() string { return "engine-supervisor" }
func (s *Supervisor) Run(ctx context.Context) error
func (s *Supervisor) Request(req RestartRequest) error
func (s *Supervisor) Status() RestartStatus
```

`BuildComponents` appends the **supervisor** where it appends the engine
today (`components.go:135`); the engine is no longer an `app.Component`.
Everything else in that function keeps its `engine` pointer unchanged (D4).
Consequence to accept deliberately: `BuildInfo.Components`
(`components.go:201-205`) — rendered verbatim by the console — will read
`engine-supervisor` instead of `engine`. That is the honest name for what
runs; the Overview/System "Components" cell needs no code change.

`pending` is computed by a `Settings.Subscribe` callback: on every swap,
`strategy.DiffAny(running, new)` → if any changed path is restart-scoped,
`PendingVersion = new.Version`, `PendingReasons += "platform settings v8"`,
state `pending`, publish. Applying a hot-only change (allowlist) never
raises the banner.

**One restart banner, two documents.** `scanner.workers` is the *strategy*
document's only restart-scoped field (`params.go:40`; `engine.go:379-384`
copies it into `scn.Cfg` once at run entry). Before this design "on restart"
meant "redeploy"; now it means "click the button in Settings", so the
supervisor also registers a `strategy.Service.Subscribe` callback comparing
the new `Scanner.Workers` against the running scanner's
`CurrentConfig().Workers` and appends `"strategy v13: scanner.workers"` to
`PendingReasons`. Without this, BL-14's `on restart` chip would be a
dead end: the operator sees the tag, gets no banner, and never learns the
change did not take effect. The banner copy in §4 already generalizes over
both sources; BL-14's chip tooltip reads *"applies on the next engine
restart — Settings → Restart engine"*.

### 2.4 Restart sequence

1. `Request` validated (§2.5) and enqueued; state → `restarting`, published
   on `health`, `engine.restart` audited, INFO alert emitted.
2. If `StopRecording`: `Recorder().Stop()` **before** cancelling. Both paths
   drain the last segment identically (`marketdata/control.go:103-105`), but
   Stop-first returns the session id for the audit entry and gives a
   deterministic "recording stopped by restart" event.
3. Record `wasRunning := Paper().Running()` (E9), then `Paper().Pause()` —
   no new cycles enter the queue while legs settle.
4. Cancel the child context; **wait for `Engine.Run` to return** (bounded by
   `Grace`), then `EndPaperSession(outgoing, now)` (E10). Strictly
   sequential: re-entering early gives two feeds racing on `e.scn`.
   Timeout → state `failed`, CRITICAL alert, no re-entry.
5. Loop re-enters: `ApplySettings(Settings.Current())`, new child ctx,
   `go Engine.Run(child)`, state → `ready` on first successful bootstrap
   (`Engine.Status().Ready`), `ReadyAt` set, `Restarts++`, published. If
   `wasRunning` was false, `Paper().Pause()` again once the engine is ready.

### 2.5 Guard rails and the failure state

`Request` refuses, in order, with a 409 and a code:

| Condition | Code | Message |
|---|---|---|
| no engine in this profile | `engine_absent` (404) | "engine not running in this profile" |
| campaign run in progress | `campaign_running` | "campaign run {id} is in progress" |
| recorder running and `stop_recording` false | `recording_active` | "recording session {id} is active — stop it, or resend with stop_recording" |
| state already `restarting` | `restart_in_progress` | "a restart is already in progress" |
| body confirm token ≠ `"RESTART"` | `confirm_required` (400) | "type RESTART to confirm" |

A campaign replays files and is not corrupted by a restart, but it saturates
a core for minutes and its verdict's provenance would straddle two settings
versions — refuse, do not offer a force flag. Refusals are audited as
`engine.restart.refused`.

**Failure policy.** If `Engine.Run` returns an error while the parent
context is alive:

- on the **first** run (`Restarts == 0`) the supervisor returns the error —
  today's fail-fast boot semantics are preserved exactly;
- after a restart (`Restarts > 0`) it enters state `failed` with the error
  string, emits a CRITICAL alert, and **stays alive without an engine**, so
  the API keeps serving and the operator can roll the settings back and
  retry. It does not auto-retry, does not auto-roll-back, and does not take
  the process down. The apply-time dry-run (D8) is what keeps this state
  nearly unreachable.

### 2.6 Health topic

`health` moves out of `Engine.Run` (`engine.go:484-486`) into the wiring, so
its snapshot can include supervisor state (which the engine cannot see):

```json
{"engine": {…EngineStatus…}, "mode": "PAPER",
 "restart": {"state":"pending","settings_version":7,"pending_version":8,"restarts":1}}
```

`scanner` and `recordings` stay engine-scoped (overwrite-on-restart is
correct for them).

## 3. API

| Method | Path | Permission | CSRF |
|---|---|---|---|
| GET | `/api/v1/platform/settings` | `view:system` | — |
| GET | `/api/v1/platform/settings/versions?limit=` | `view:system` | — |
| GET | `/api/v1/platform/settings/version/{n}` | `view:system` | — |
| POST | `/api/v1/platform/settings/preview` | per changed section | yes |
| POST | `/api/v1/platform/settings` | per changed section | yes |
| POST | `/api/v1/platform/settings/rollback` | per changed section | yes |
| POST | `/api/v1/engine/restart` | `system:config` | yes |
| GET | `/api/v1/engine/restart` | `view:system` | — |

"per changed section" = `strategy.TopLevelSections(diff)` →
`platform.PermissionForSection`: `venues` → `PermExchangeConfig`,
`paper`/`telegram` → `PermSystemConfig`. Both are ADMIN-only today; the
mapping exists so a future role can hold one without the other. The check
runs **inside** the service's writer lock via `ApplyAuthorized`.

```jsonc
// GET  /api/v1/platform/settings → {version, created_by, created_at,
//        settings, plan, restart:RestartStatus,
//        field_timing: {"venues.binance.symbols":"restart","telegram.allowlist":"hot"}}
// POST /api/v1/platform/settings          {"settings": {…}, "parent_version": n}
//   parent_version is REQUIRED (review P3(g), T-058): every web-console
//   apply/rollback follows a GET, so the client always has a real
//   version to echo back; omitting it is 400 parent_version_required,
//   not a silently unchecked write. Telegram/system callers go through
//   ApplyAuthorized directly (expectedParent=0), not this HTTP path, so
//   they are unaffected.
// POST /api/v1/platform/settings/preview  {"settings": {…}}  → identical body, no write, no parent_version needed
//   → {"diff": {"venues.binance.symbols": {"old":"[…]","new":"[…]"}},
//      "sections": ["venues"], "requires_restart": true,
//      "plan": {"markets":7,"triangles":6,"rejected_untradeable":2}}
// POST /api/v1/engine/restart
//        {"confirm":"RESTART","reason":"apply symbols v8","stop_recording":false}
//   → 202 {"restart": {…RestartStatus…}}
```

`diff` is `Record<string,{old,new}>` — the same wire shape the Strategies
page already receives, so the confirm dialog's diff table (BL-02/BL-03) is
one component for both documents.

`field_timing` is served by the backend so the frontend never hardcodes
which fields are hot — the mistake `docs/design/console-ux-audit.md` §1.13
records for campaign verdict severities.

Errors: `400 invalid_settings` (message verbatim from `Validate`, path-first
so the form can highlight the field, exactly like `params.go`),
`400 unknown_symbol` (names the symbol and the venue), `400 no_triangles`
("this symbol/asset combination closes no triangle on {venue}"),
`400 no_change`, `403` from the section gate, `409` per §2.5,
`503 settings_unavailable` when the service is absent.

Audit entries (`audit_events`, insert-only): `settings.apply` and
`settings.rollback` (`entity=platform_settings`, `entity_id=version`,
before/after full payloads), `engine.restart` (`entity=engine`, after
`{reason, stop_recording, recording_stopped_id, settings_version}`),
`engine.restart.refused` (after `{reason, refusal_code}`),
`engine.restart.failed` (after `{error}`).

## 4. Frontend contract

Settings gains two of the five §2.2 sections. Both render the shared
per-field tag: a dim `Immediate` chip or an amber `On restart` chip, driven
by `field_timing`.

**Markets & assets** (BL-13b) — multi-select of the venue's tradeable
symbols (from the catalog, searchable), chips for starting assets, a
per-asset paper-balance input (decimal string, never parsed to a number),
and a live line: *"6 markets → 4 triangles (2 rejected: untradeable)"* from
`preview`. Every field tagged **On restart**.

**Venues & fees** (BL-15) — per venue: `Enabled`, `Paper enabled`, maker/
taker bps, a per-symbol override table (add/remove rows), and the token
discount toggle with the venue's fixed rate shown as read-only text
(*"BNB — 25% off, applies to API trades"*). Every field **On restart**. The
audit's honest "not yet editable" placeholder is deleted only when this
ships.

**Notifications** gains the Telegram allowlist (chips of user IDs), tagged
**Immediate** — or **On restart** with the note *"the bot was started
without an allowlist; adding the first user needs a restart"* when
`field_timing` says so. The bot token is never shown or accepted.

Confirmation follows the audit's §3.3 pattern, reusing `ConfirmDialog`
(BL-02):

> **Apply new platform settings?**
> This becomes version **v8**. The Telegram allowlist change takes effect
> immediately. The other changes are saved now and take effect when you
> restart the engine.
>
> | Setting | Current (v7) | New (v8) | Effect |
> |---|---|---|---|
> | binance · symbols | 6 symbols | 7 symbols | on restart |
> | binance · taker fee | 10 bps | 7.5 bps | on restart |
>
> New topology after restart: **7 markets → 6 triangles**.
>
> [Cancel] [Apply v8]

Restart banner (page-level, above the sections, and on Overview's status
strip) when `restart.state == "pending"`:

> **Saved but not running: platform settings v8; strategy v13
> (scanner.workers).** Restarting reconnects the market-data feed, rebuilds
> the triangles, and starts a **new paper session** — persisted
> opportunities, cycles and reports are kept; in-memory paper balances reset
> to the configured starting balances. A paused paper engine stays paused.
> [Restart engine…]

The headline is `restart.pending_reasons` joined verbatim, never a
frontend-composed sentence (the string-matching mistake of audit §1.13).

The restart dialog is type-to-confirm `RESTART` (the paper-reset pattern,
audit §4.3), carries an optional reason, and shows a
`Stop the active recording ({id}, running 41m) and restart` checkbox only
when `recording_active` applies. While `state == "restarting"` the banner
reads *"Restarting — waiting for the last recording segment to close and
the feed to drain…"*; on `failed` it renders the backend error verbatim in
`bad` tone with a **Roll back to v7** action. Never a green/neutral
placeholder for an unknown state.

## 5. Tests and migration

Unit — `internal/platform`:
- `Validate` table: one case per row of §1.3 (both bounds), plus zero
  venues enabled, balance key set mismatch, duplicate symbols, `PAPER` mode
  without `paper_enabled`, discount toggle on an API-ineligible venue.
- `Clone` mutation-independence (the `strategy.Params.Clone` audit-P3 test,
  reused shape).
- `ValidateAgainstCatalog` against a fake catalog: unknown symbol, starting
  asset absent from the symbol set, symbol set that closes no triangle,
  and a happy path asserting `Plan.Triangles`.
- `Seed(Bootstrap)` reproduces today's defaults exactly (a fresh install
  behaves identically to before this feature — the `DefaultParams` rule).

Unit — `internal/app/supervisor_test.go` with a fake `EngineRunner` that
records `ApplySettings` calls, blocks until its ctx is cancelled, and can be
told to fail: re-entry happens **only after** the previous `Run` returned
(the fake panics otherwise); `stop_recording` calls `Stop()` before the
cancel; a paused paper engine is still paused after the restart (E9);
refusals for campaign-busy / recorder-active / double request; second-run
failure → `failed` with the supervisor alive and a later `Request` accepted,
while a first-run failure returns the error; stop exceeding `Grace` →
`failed`, no re-entry; `PendingReasons` gains an entry for a restart-scoped
settings change **and** for a `scanner.workers` change, and gains none for
an allowlist-only change.

API — `internal/api/platform_test.go`, fakes only (existing style): RBAC
403 for VIEWER and OPERATOR, CSRF rejection, missing confirm token, 409
bodies, audit sink assertions, and `field_timing` presence.

Integration — `internal/app/engine_restart_test.go`: real `Engine` +
`Supervisor` + memory stores, `Engine.RESTHost` (new field, defaulting to
`binance.MarketDataRESTHost`) pointed at an `httptest` server serving a
small `exchangeInfo`, `WSHost` at a dead address (the feed retries with
backoff and never fails the run, `binance/feed.go:91-95`). Assert:
`Status().Markets` reflects v1; apply v2 with a different symbol set;
`Request` restart; `Status().Markets` reflects v2, `Ready` returns true,
rows written under the first session are still readable, and the first
session row has a non-NULL `ended_at` (E10).

Migration: `migrations/000006_platform_settings.{up,down}.sql` (§1.4).
Down drops the table only. No backfill: `Load` seeds v1 from the
environment on first boot, which is exactly today's behaviour.

## 6. Non-goals and safety

- **No live trading, no keys.** No field, route, table column or UI control
  in this design accepts, stores, displays or masks an exchange API key or
  secret — stronger than SKILL §42's "never display complete API secrets
  after entry", because none are ever entered. `LiveExecutor` still returns
  `ErrLiveTradingDisabled`; nothing here can flip it.
- **Risk engine untouched.** No file under `internal/risk`, `internal/pricing`,
  `internal/opportunity` or `internal/simulation` is modified. Risk limits
  stay in `strategy.Params` behind `PermRiskConfig`. Settings cannot widen a
  limit, and the AI advisor gets no route into this document.
- **Money math.** bps are `decimal.Decimal`, balances are decimal strings
  end to end; the only conversion is `bps.Div(10000)` into `fees.Rate`.
- **History is never lost.** Restart preserves every persisted row (E3 + the
  outbox drain) and closes the outgoing paper session cleanly (E10) so the
  boundaries stay readable too. The only reset is in-memory paper balances,
  stated in the confirm copy. No `DELETE` or `TRUNCATE` anywhere in this
  feature.
- **Stays env / out of scope:** `ARB_RECORDING_DIR` (changing it orphans
  `market_recording_metadata.segment_files`), `ARB_ADMIN_EMAIL/PASSWORD`
  (settled by the audit §3.1 — a process needs one credential before a UI
  can create any), `ARB_DATABASE_URL`, `ARB_MODE`, the Telegram bot token,
  and the Anthropic key. Per-triangle enable/disable (`triangles.enabled`)
  is BL-26, not this task. No cross-exchange anything: `Venues` is a map so
  a second connector (T-050) drops in, but each cycle stays on one venue.
- **Not built:** no scheduled/automatic restarts, no rolling restart, no
  multi-process supervision, no config import/export files.
