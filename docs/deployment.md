# Deployment: recording real feeds and running the §80 campaign

This runbook takes the platform from a fresh host to a completed
profitability-validation campaign (T-046). Everything runs in Docker
Compose; the only external dependency is outbound HTTPS/WSS to Binance
(`api.binance.com`, `stream.binance.com:9443`). No API keys are needed —
recording uses public market-data endpoints only, and the platform never
places orders by design (`ErrLiveTradingDisabled`).

## 1. Prerequisites

- A host with Docker + Compose v2, outbound internet, and disk for
  recordings (see sizing below).
- This repository checked out.
- A `.env` next to `docker-compose.yml` (copy `.env.example`). It is
  not optional: the database has no default password and compose
  refuses to start until `POSTGRES_PASSWORD` is set.

```dotenv
POSTGRES_PASSWORD=<output of: openssl rand -base64 24>
ARB_SYMBOLS=BTCUSDT,ETHUSDT,ETHBTC,BTCUSDC,ETHUSDC,USDCUSDT
ARB_STARTING_ASSETS=USDT,USDC
# only for the paper profile:
ARB_ADMIN_EMAIL=you@example.com
ARB_ADMIN_PASSWORD=a-strong-password
```

Scope of this stack: one operator on one host. Postgres is published
on `127.0.0.1:5432` only (host tools reach it, nothing off-host can),
and the API and console on `:8080` are plaintext HTTP, so keep them on
loopback or put a TLS-terminating reverse proxy in front before
exposing them. `docker compose config` shows the database publication
with `host_ip: 127.0.0.1`; there is no `0.0.0.0` binding for 5432.
Multi-user or internet-facing deployments use the Kubernetes path in §6.

## 2. Start recording

```sh
make record          # = docker compose --profile record up -d --build
```

This builds the image, starts PostgreSQL, applies all migrations, and
launches `arbd` in RECORD mode. The recorder captures every raw WS depth
frame and REST snapshot for the configured symbols into rotated,
zstd-compressed segment files under the `recordings` volume, and
registers each closed segment in `market_recording_metadata`.

Find the session id (you need it for the campaign):

```sh
docker compose logs arbd-record | grep "recording enabled"
# ... recording enabled dir=/recordings session=<SESSION-ID>
```

### Verify it is actually capturing

```sh
# frames flowing and no resync storms:
curl -s localhost:8080/healthz
docker compose exec arbd-record ls -lh /recordings/<SESSION-ID>/

# segment rows registering (repeat later; the list should grow):
docker compose exec db psql -U arb -d arb \
  -c "SELECT id, started_at, ended_at, jsonb_array_length(segment_files) AS segments
      FROM market_recording_metadata ORDER BY started_at DESC LIMIT 5;"
```

### Sizing and duration

Six liquid symbols at 100 ms depth updates produce very roughly
0.5–2 GB/day compressed, dominated by BTC/ETH volatility. For a first
campaign record **at least a few hours spanning active market hours**;
for numbers worth arguing about, record **multiple sessions across
different days and regimes** (calm, volatile, weekend). Segments rotate
every 200k frames; a crash loses at most the open segment.

Stop when done:

```sh
make record-stop
```

## 3. Run the §80 campaign

```sh
make campaign RECORDING=<SESSION-ID>
```

This replays the recording through the complete pipeline — recorded
books → the real scanner → the deterministic risk engine → the simulated
executor → portfolio — once per (scenario × seed) over the §80 stress
grid:

| axis | scenarios |
|---|---|
| higher fees | +5 bps, +10 bps per leg |
| higher latency | ×2, ×4 |
| worse fills | executor sees 50% of displayed depth |
| lower liquidity | the whole world at 50% depth |
| combined | fees +10, latency ×3, both depth cuts |

Outputs land in the recordings volume:
`campaign-<SESSION-ID>.md` (the report) and `.json` (raw evidence).
Copy them out with:

```sh
docker compose cp arbd-record:/recordings/campaign-<SESSION-ID>.md .
```

The report's **Verdict** section carries the honest §80 judgement,
including the mandatory flag when the strategy is profitable only under
perfect conditions — a system like that is worthless and the report says
so. One recording is one sample: repeat over several recordings before
trusting any positive result.

### Fidelity notes (what the simulation does and does not claim)

- Fills are simulated with seeded latency against books that **continue
  to move during simulated waits** (frames recorded inside the wait
  window are applied before the fill is priced), so adverse drift is
  modeled from real data.
- Limit-IOC semantics, partial fills, fee-in-kind, quantization, and
  intermediate exposure follow the audited simulation engine — the same
  code paper trading runs.
- Self-impact is NOT modeled: simulated orders do not consume the
  recorded book for other participants, and the recording obviously
  contains no reaction to orders that were never sent. Treat results as
  an upper bound and lean on the stressed scenarios, not the baseline.

## 3b. Console-driven recording and campaigns

Everything in §2–§3 can also be driven from the operations console —
no terminal needed once the stack is up:

```sh
docker compose --profile paper up -d --build   # engine + API on :8080
cd web && npm run build && npm start           # console
```

Sign in and open **Campaigns**:

- **Recorder** card — start/stop an in-process recording session
  (permission `recordings:control`, OPERATOR and ADMIN). The session id,
  uptime, frames written/dropped and closed segments update live; stop
  waits for the last segment to close and register, so the session is
  immediately usable. `ARB_MODE=RECORD` still auto-starts a session at
  boot for headless deployments.
- **Recorded sessions** — the `market_recording_metadata` catalog with
  a *Run campaign* button per closed session.
- **Run §80 campaign** — the same grid × seeds the CLI runs
  (`internal/campaign` is shared by both), executed in the background
  inside `arbd` (permission `campaigns:run`); progress streams over the
  console WebSocket. Reports are written next to the recording
  (`/recordings/<SESSION-ID>/campaign-<RUN-ID>.md|.json`) and kept in
  `campaign_runs`, and the **Verdict** flags are shown verbatim.

Every start/stop/run is audited (`audit_events`, source=web). Recording
still needs no API keys and the engine still cannot place orders.

## 3c. Settings & restart from the console

Symbols, starting assets, per-asset paper balances, venue enablement and
fees, and the Telegram allowlist live in a second versioned document
(`internal/platform`, table `platform_settings`) — separate from the
strategy config in §80's campaign grid, and separate from
`ARB_SYMBOLS`/`ARB_STARTING_ASSETS`/`ARB_PAPER_BALANCE`/
`ARB_TELEGRAM_ALLOWLIST`, which only seed **version 1** on a fresh
install and are ignored afterward. Open **Settings** in the console:

- **Markets & assets** and **Venues & fees** edit the document (ADMIN
  only: `exchange:config` for venues/symbols/fees, `system:config` for
  paper balances and the allowlist); every field there is tagged
  **On restart**. A **Preview** call (`POST
  /api/v1/platform/settings/preview`, `view:system`) dry-runs the
  topology (`graph.Build`) against the live `exchangeInfo` before you
  can apply, so a bad symbol list is rejected on save, never discovered
  when the engine restarts. `token_discount` is rejected outright at
  validation (no pay-asset debit ledger exists yet, so the discounted
  fee rate would make paper P&L optimistic with nothing to account for
  it); `GET /api/v1/platform/venues` (`view:system`) serves the
  compiled venue/discount table (pay asset, rate, whether it applies to
  API trades, and an honest `modeled: false`) so the console renders —
  and correctly disables — that toggle from data instead of hardcoding
  it.
- **Notifications** carries the Telegram allowlist, tagged **Immediate**
  once the bot is running — revoking a user takes effect without a
  restart. If the process booted with an empty allowlist the bot was
  never constructed; adding the first entry still needs a restart.
- **Restart engine…** (`POST /api/v1/engine/restart`, `system:config`,
  type `RESTART` to confirm) applies every pending restart-scoped
  change: it stops an active recording first only if you check that
  box, pauses paper trading while legs settle, waits for the current run
  to drain (persisted opportunities/cycles/reports are never touched),
  then reconnects the feed and rebuilds the triangles under one stable
  engine process — never a process restart. It refuses (409) while a
  campaign run is in progress or a recording is active without
  `stop_recording`, and while a restart is already under way.

## 3d. Operating mode, AI advisor, log level, origin and secrets (T-059..T-061)

The same versioned document (`platform_settings`) now also carries the
fields that used to be env-only. Every one of them is validated,
versioned, audited, and tagged by the backend's `field_timing` as either
**Immediate** (hot) or **On restart**; the environment seeds **version 1
only** and is ignored afterward (the boot log says so).

| Setting | Path | Timing | Notes |
|---|---|---|---|
| Operating mode | `platform.mode` | On restart | `MARKET_DATA`, `RECORD`, `PAPER`. `SHADOW` is listed but `available:false` (not wired into the engine). `LIVE` is refused by name — this platform never places real orders; `REPLAY`/`BACKTEST` are batch runs started from Replays/Campaigns, not process modes. An `ARB_MODE` of `SHADOW`/`REPLAY`/`BACKTEST` seeds `MARKET_DATA` and logs the substitution; so does a pre-expansion document whose stored venues cannot run under the env mode (e.g. `paper_enabled=false` with `ARB_MODE=PAPER`) — the boot log names it, and the next operator write persists the complete document. |
| Log level | `platform.log_level` | Immediate | `debug|info|warn|error`. `debug` logs every rejected opportunity — a measurable cost on the consumer path. |
| Console origin | `platform.allowed_origin` | Immediate | Strict `scheme://host[:port]`. Same-host origins are always admitted, so a wrong value cannot lock the console out. |
| AI advisor | `ai.*` | Immediate | `enabled`, `provider` (`anthropic` or `fake`; `openai` is listed as not built), `model`, `schedule.{hourly_minutes,daily_hours,weekly_hours}` (0 disables a cadence), `budget.{max_analyses_per_day,max_output_tokens}`. The daily cap is a per-process UTC counter and resets on a process restart. Enabling `anthropic` without a resolvable key is accepted and answered with a `warnings` entry; `GET /api/v1/ai/status` then reports `enabled:true, running:false, reason:"no anthropic_api_key"`. |
| Telegram mute | `telegram.disabled` | Immediate | Mutes commands and pushes together. The field is deliberately negative so documents written before it existed keep delivering. |

Routes (all under `/api/v1`, `view:system` to read, `system:config` +
CSRF to write):

- `GET /platform/capabilities` — `{modes, venues, ai_providers,
  log_levels, secrets:{vault_configured,key_id|reason}, field_timing}`.
  Every list entry is `{id, available, reason?}`; the console renders
  from this, never from hardcoded enums. `GET /platform/venues` is an
  alias over the same venue table (binance available; okx/bybit/bitget/
  gate/mexc listed with the task that blocks them). Enabling an unbuilt
  venue is `400 connector_unavailable` naming that task.
- `GET /ai/status` (`view:dashboard`) — `{enabled, running, reason,
  provider, model, key_source, analyses_today, max_per_day,
  last_analysis}`.
- `GET /system/status` reports the **running** mode; the `health` hub
  topic carries `mode: {running, configured}` so a pending mode change
  can be annotated until the restart applies it.

**Changing the mode.** Apply the new `platform.mode`, then **Restart
engine…**. Leaving `RECORD` with an active recording is refused
(`409 recording_active`) unless you tick *Stop the active recording*;
the Supervisor then stops it before cancelling so the last segment
closes cleanly. Entering `PAPER` requires `paper_enabled` on every
enabled venue and a `paper.balances` entry per starting asset — both
are validated at apply time, never discovered at restart — and the new
run mints a fresh paper session. Leaving `PAPER` closes the outgoing
session; persisted cycles and opportunities are kept, in-memory balances
reset.

**Secrets vault.** Provider credentials (`anthropic_api_key`,
`telegram_bot_token` — a closed registry; no exchange key can be stored)
live in the `secrets` table (migration 000008), encrypted with
AES-256-GCM under `ARB_SECRET_KEY`. Generate a key once and keep it out
of the repo:

```sh
make create-secret   # writes ARB_SECRET_KEY to .env (32 random bytes, base64); keeps an existing key
```

- Unset or invalid key → the vault stays closed (logged at boot), the
  platform keeps running on the env-provided values, `GET /secrets`
  answers `vault_configured:false` with the reason, and writes answer
  `503 vault_unavailable`. The vault also needs `ARB_DATABASE_URL`.
- `GET /api/v1/secrets` (`view:system`) lists presence, source
  (`vault`/`env`), readability, `updated_at`/`updated_by` and `applies`
  — never a value, never a prefix.
- `PUT /api/v1/secrets/{name}` `{"value":"…"}` (`system:config`, CSRF)
  stores the value (trimmed; printable ASCII; ≥20 and ≤4096 chars) and
  audits `secret.write`. The Anthropic key applies **immediately**
  (the advisor is rebuilt on the next settings swap and on the write);
  the Telegram token applies on the next **process** restart — the
  response's `applies` field says which.
- `DELETE /api/v1/secrets/{name}` removes the row so resolution falls
  back to the environment; audited as `secret.delete`.
- A row written under a different `ARB_SECRET_KEY` is reported
  `present:true, readable:false, reason:"encrypted under key …; this
  process holds …"` and resolution falls through to env. Rotating the
  master key is manual: `DELETE` every row, restart with the new key,
  `PUT` every value.

### Exchange API credentials (Settings → Security → Exchange API credentials)

The same vault holds, per venue, the API key / secret (and passphrase
for OKX and Bitget) so they never sit in `.env`. `applies` is
`not_consumed`: the backend refuses to resolve the exchange group and no
component reads the values — the platform consumes public market data
only and live trading is disabled by design. Create the keys on the
exchange as **read-only** (no trade, no withdrawal). The vault must be
open (`ARB_SECRET_KEY` set) for writes, as for every secret.

### What still comes from the environment, and why

After T-057..T-061 the console owns every operating parameter. What
remains in `.env` is process bootstrap that cannot live in the database
it configures:

| Variable | Why it stays env |
|---|---|
| `ARB_DATABASE_URL` | Locates the database the settings live in. |
| `ARB_SECRET_KEY` | Master key for the vault; storing it in the vault is circular. |
| `ARB_HTTP_ADDR`, `ARB_METRICS_ADDR` | Listen addresses; bound before the database is reachable. |
| `ARB_RECORDING_DIR` | Filesystem path of the recordings volume (container mount). |
| `ARB_ADMIN_EMAIL`, `ARB_ADMIN_PASSWORD` | First-boot admin bootstrap only; ignored once a user exists. |
| `ARB_SHUTDOWN_GRACE` | Read during shutdown; never a trading parameter. |
| `ARB_REPLAY_SESSION`, `ARB_SEED` | CLI batch tools only (`replay`/`campaign` binaries); the console passes them per job. |
| `ARB_MODE`, `ARB_SYMBOLS`, `ARB_STARTING_ASSETS`, `ARB_PAPER_BALANCE`, `ARB_LOG_LEVEL`, `ARB_ALLOWED_ORIGIN`, `ARB_AI_*`, `ARB_TELEGRAM_*`, `ANTHROPIC_API_KEY` | **Seed version 1 only**; ignored once the settings document exists. Safe to delete from `.env` after first boot. |

## 4. Optional: full paper deployment

```sh
docker compose --profile paper up -d --build
```

Runs the complete engine (scanner, risk, paper trading, API on :8080)
against live feeds, with the console servable separately via
`cd web && npm run build && npm start`. Set the admin credentials in
`.env` first; the API refuses logins without configured users. Same
single-operator scope as §1: the API stays plaintext on `:8080` and
Postgres stays on loopback with the password from `.env`.

## 5. Operational notes

- `/metrics` (Prometheus) is RBAC-gated on the same port; set
  `ARB_METRICS_ADDR` for a private unauthenticated exporter port
  instead when scraping from inside the network.
- The recorder degrades honestly: overflow drops are counted
  (`recorder_frames_dropped`) rather than stalling the feed; sequence
  gaps trigger book resyncs which the recording itself captures.
- Postgres is the source of truth for market metadata (instrument
  rules), the recording catalog, and the stream table the campaign
  needs — keep the `pgdata` volume alongside the `recordings` volume.

## 6. Environments

Three environments run the same image digests with per-environment
configuration only. Everything is code under `deploy/`
(`deploy/helm/arb-platform`, `deploy/terraform`, `deploy/postgres`,
`deploy/observability`) and `.github/workflows/deploy.yml`.

| | dev | paper-test | prod |
|---|---|---|---|
| Where | docker compose (§1–§4) or a local cluster with `values-dev.yaml` | managed k8s, `values-paper-test.yaml` | managed k8s, `values-prod.yaml` |
| Postgres | compose `db` (loopback only, password from `.env`) / in-cluster | managed HA (multi-AZ), 7-day provider PITR | managed HA (multi-AZ) + read replica, 14-day provider PITR; weekly restore drill into a scratch instance (`docs/runbooks/restore-drill.md`); the pgBackRest manifests apply to a self-hosted tier only |
| Secrets | `.env` (never committed) | External Secrets from the KMS store, prefix `arb/paper-test/` | same, prefix `arb/prod/` |
| Ingress | none | TLS (staging issuer), WAF + rate limit | TLS, WAF + rate limit, canary annotations |
| arbd | 1 replica | 1 replica, ServiceMonitor on | 1 replica (single-writer), anti-affinity, zone spread, PDB maxUnavailable=0 |
| web | 1 | HPA 2–4 | HPA 3–10, PDB minAvailable=2 |
| `ARB_MODE` | PAPER / RECORD | PAPER | PAPER |

Promotion flow (`deploy.yml`):

1. Tag `vX.Y.Z` -> build `arbd` and `web` images once, push by digest,
   Trivy scan (fail on critical), cosign keyless signature, chart lint
   and template for every values file (including the negative check
   that `arbd.mode=LIVE` is rejected).
2. `paper-test`: `helm upgrade --atomic` with the digests; the migrate
   hook runs first and a failure aborts the release. Smoke test
   (`deploy/scripts/smoke.sh`: rollout, `/healthz`, `/readyz`, console,
   mode check). Failure -> `helm rollback`.
3. `prod-approval`: a GitHub environment with required reviewers. A
   human approves the exact digests that passed paper-test.
4. `prod-canary`: second release `arb-canary`
   (`deploy/helm/canary-values.yaml`) — the new arbd in PAPER mode
   against live feeds with persistence disabled (in-memory: no DSN is
   projected, `ARB_DATABASE_URL` is pinned empty, and the chart refuses
   a canary that references the primary's database key, runs any other
   mode, enables the migrate hook, claims the recordings volume or takes
   weighted traffic) plus the new console. It takes no share of user
   traffic: reviewers reach it with the `X-Arb-Canary: always` header,
   because its sessions and settings are separate from the primary's.
   `deploy/scripts/canary-check.sh` proves rollout, `/healthz`,
   `/readyz`, the isolation (empty `ARB_DATABASE_URL` or a different
   database target than the primary, no shared secret key, no canary
   weight) and fresh `orderbook_age_ms` series; then a 15-minute bake
   polls Alertmanager (`deploy/scripts/bake.sh`); any of
   `APIAvailabilityBurnFast`, `FeedStale`, `PodCrashLooping`,
   `MigrateJobFailed`, `ArbdNotReady` firing uninstalls the canary and
   stops the pipeline. `deploy/helm/test-canary-guards.sh` proves the
   chart refusals with `helm template` in the chart job.
5. `prod-full`: atomic upgrade of the main release, smoke, 10-minute
   bake, canary removed. Failure -> automatic `helm rollback` to the
   previous revision followed by a smoke test of the rolled-back state.

Every step is reversible: images are immutable digests, releases keep
five revisions, migrations are forward-only in the hook and reversed by
hand per `docs/runbooks/restore-drill.md`. Cloud access in CI uses
OIDC-assumed roles configured as repository variables; the workflow
file contains no secrets and the application only ever sees secrets
through External Secrets.

SLOs (30-day windows, rules in `deploy/observability/platform-rules.yml`):
API availability 99.9 %, API p99 latency < 500 ms, feed freshness
99.5 % of minutes with max book age < 5 s. Burn-rate alerts gate the
prod bake.

LIVE execution is disabled in every environment by code. There is no
LIVE value for `ARB_MODE`: the chart's `arb.mode` helper fails the
render on anything other than RECORD, PAPER or REPLAY, the values files
pin PAPER, the smoke test re-checks the rendered ConfigMap, and the
executor returns `ErrLiveTradingDisabled`. Enabling live orders is the
production gate in `docs/design/crypto-arb-platform-command.md` — a
reviewed code change after 30+ days of positive paper evidence in
`docs/campaigns/`, a security review, and a recorded legal decision —
never a configuration, secret, or pipeline input.
