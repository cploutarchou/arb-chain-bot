# Client-area refinement — backend contract review (T-087)

A read-only review of the Go backend, commissioned before any copy was
written, so the console describes what the backend actually does. No
backend change was made and none is proposed here. Every claim below is
grounded in source; the frontend decisions that follow from it are in the
right-hand column.

## 1. Paper pause/resume scope — the most important finding

`POST /api/v1/paper/pause|resume` (`internal/api/server.go:340-355`)
reaches `PaperController` → `paperProxy` → `e.Paper()` → `*paper.Engine`
(`internal/paper/engine.go:275-276`), which is **the triangular
arbitrage paper engine only**. The implementation is one flag.

Three things the UI must therefore not imply:

| Fact | Evidence | Console consequence |
| --- | --- | --- |
| Pausing does **not** pause Scanner Suite or rule-based auto-paper | `internal/screener` has no dependency on `internal/paper`; `Automation.TickOnce` gates only on `Collectors.Running()`; **there is no pause route for it at all** (`internal/api/screenerapi.go:61-101`) | the control is labelled as scoped to triangular simulations, and Rule simulations state plainly that they are not covered by it |
| Pause is **process-wide**, not per-organisation | `paperGate` never reads `p.OrgID`; one shared `*Engine` (`internal/app/components.go:587`) | never call it "my simulations" |
| Pause stops **new** cycles; one already in flight can still settle | `internal/paper/engine.go:1-32` package doc | never imply an instant freeze |

RBAC tiers differ: pause/resume is `PermPaperControl` (OPERATOR+,
`internal/auth/rbac.go:51-57`); reset is `PermPaperReset` (ADMIN only,
`rbac.go:63`) and additionally requires a `{"confirm":"RESET"}` body.

**This is the one place a backend change would be needed** for a
genuinely global pause. Not made, not requested here: it would mean a new
endpoint or a shared flag checked by `Automation.TickOnce` and
`paperexec.Executor.Tick`. The console instead states the real scope.

## 2. `platform_admin` — gating platform settings is frontend-only work

`platform_admin` **is** populated on `/auth/me`
(`internal/api/auth.go:185-198`) and is a field independent of both
`role` (ADMIN/OPERATOR/VIEWER) and `org_role`
(OWNER/ADMIN/MEMBER/VIEWER).

`requirePlatformAdmin` (`auth.go:535-548`) guards platform settings,
the secrets vault, user creation, org creation/override, engine restart,
billing price mapping and `/metrics`. A tenant OWNER/ADMIN receives
`403 platform_admin_required` on those routes regardless of package,
asserted in `internal/api/tenancy_scope_test.go`.

So hiding platform administration from a tenant ADMIN in the console is
**presentation catching up with a gate the backend already enforces** —
no backend work, and nothing is being loosened.

Two things recorded as **questions for the operator**, not findings:

1. `requirePerm` checks the **global** `role`, never `org_role`
   (`auth.go:635-648`). `PermScreenerConfig`, `PermPaperReset`,
   `PermRiskConfig`, `PermExchangeConfig`, `PermUserManage` and
   `PermSystemConfig` are ADMIN-only. So `org_role` cannot be used by
   the console to predict whether a form will be writable; `me.role` is
   the discriminator.
2. The only two writers of `users.platform_admin`
   (`internal/storage/authstore.go:174,262-263`) set it unconditionally
   to `role == ADMIN`. Whether real provisioning can produce a tenant
   admin with `role=ADMIN` and `platform_admin=false` is not something
   the code settles. If it cannot, tenant orgs cannot self-serve Scanner
   Suite settings today. **No security finding is asserted**, and no
   existing audit conclusion is reopened; this is flagged for the
   operator's decision.

## 3. Zero qualified candidates — there is an honest explanation

The Overview counters `evaluations` / `qualified` / `rejected` come from
one `GET /api/v1/scanner/status`, are cumulative for the current engine
run, and are snapshotted together. They are the **same interval but not
the same stage**:

```
Evaluations = SkippedBooks + NoViableSize + Qualified + Rejected
```

That identity is a tested invariant (`internal/scanner/scanner.go:338-433`);
`SkippedBooks` (book missing/unhealthy) and `NoViableSize` (nothing
clears the dust/min-notional floor) exit **before** the risk gate, so
`detected − rejected` is **not** qualified — which is exactly the
inference the command forbids.

The two missing stages are already on the wire and simply were not
rendered: `skipped_unhealthy`, `no_viable_size` and `dropped_events` are
fields of the same `/scanner/status` payload (`internal/app/engine.go:298-302`).

A **live** rejection-reason breakdown also exists:
`GET /api/v1/risk` returns `reject_reason_counts`, a reason-code → count
map (`internal/app/readmodel.go:131-154`), in-memory, sharing the
scanner counters' reset epoch, already rendered on `/risk`.

| Stage | Reason breakdown available? |
| --- | --- |
| Skipped (unhealthy/missing book) | **No** — aggregate count only |
| No viable size | **No** — aggregate count only |
| Rejected at the risk gate | **Yes** — `reject_reason_counts` on `/api/v1/risk` |
| Qualified | n/a |

The T-062 rejection histogram (commit `bb0d4e8`) is
**backtest/campaign-only** (`internal/backtest/backtest.go`) and explains
a simulated run's verdict. It is not the live path and the two must not
be conflated.

**Console consequence:** Overview explains zero qualified by showing the
real four-way stage split and linking to the risk page for the rejection
reasons, and says explicitly that the two pre-gate stages have counts but
no reason breakdown. Nothing is synthesised and no cause is invented.

## 4. Settings anchors — the nine are exactly right

Confirmed at `web/src/app/settings/page.tsx:853-916`:
`#operating-mode #markets #scanner-suite #logging #ai #platform-versions
#users #notifications #security`.

`#security` wraps the exchange-credential **vault** (`SecretsSection`),
not a general security area — the static "Security posture" list has no
anchor. `TelegramAllowlistSection` renders inside `NotificationsSection`,
so it lives under `#notifications`. Sections with no anchor today: Setup
wizard, Session, Venues, Strategy & risk.

## 5. Overview summaries — all supported, none invented

| Summary | Supported by | Notes |
| --- | --- | --- |
| Net results per asset | `GET /api/v1/pnl` → `assets[]` | one row per start asset; **the backend performs no cross-asset sum**, and neither does the console |
| Realized vs marked | same payload | `realized` cash-basis, `exposure_mark` marked exposure, `net_pnl` their sum |
| Drawdown | same payload | `drawdown` is `StringFixed(4)` — the only fixed-precision field here |
| Active simulations | `/scanner/status` → `paper.active_simulations` | |
| Items needing attention | **composable, not one field** | `/alerts?state=active`, `/risk` breakers, `/system/health` restart + feed |
| Session vs historical | two endpoint families | `/pnl`, `/portfolio`, `/scanner/status` are session state reset on restart; `/pnl/breakdown`, `/pnl/series`, `/opportunities/history` are DB-backed and windowed |

Wire types: every `decimal.Decimal` marshals as a **quoted JSON string**
(`internal/exchange/types.go:1-6`) — which is why the console's
presentation layer works on strings and never parses them into a number.
Ratios, ages and counters are plain JSON numbers.

## 6. The two report systems and the two ledgers stay distinct

- `/api/v1/reports*` → `internal/reporting`: the triangular engine's
  daily/weekly **operational** reports.
- `/api/v1/screener/reports*` → `internal/screener/report`: the Scanner
  Suite's **evidence**/production-gate report. Different store,
  different meaning.
- `/api/v1/paper/*` → `internal/paper.Engine`'s triangular cycle ledger.
- `/api/v1/screener/auto-paper` → `paperexec.Executor`'s rule-based
  ledger (`PaperPosition`, `PaperBalance`, `RuleSummary`).

Confirmed distinct in API, ledger, metrics and controls. The navigation
brings them into one understandable family and keeps their meanings apart.

## 7. Auto-paper rule naming — join, do not fabricate

`GET /api/v1/screener/auto-paper` returns only `rule_id`; none of
`PaperPosition`, `RuleSummary` or `PaperBalance` carries a name
(`internal/screener/service_automation.go:47-92`). A human-readable
`Name` **does** exist on `screener.Rule` from `GET /api/v1/screener/rules`
(`internal/screener/rules.go:24-27`).

**Console consequence:** join client-side on `rule_id` and show the real
name when the rules endpoint supplies one; otherwise show a labelled
short identifier with the full value available. No name is ever invented.

## 8. Error envelopes — how the console tells failures apart

Every response uses one envelope, and **both keys are always present**
(no `omitempty`), so the console branches on `error !== null`, never on
key presence (`internal/api/server.go:464-467`).

| Status | `error.code` | Extra data |
| --- | --- | --- |
| 401 | `unauthenticated` | — |
| 403 | `risk_ack_required` | `required_version` |
| 403 | `forbidden` | — |
| 403 | `platform_admin_required` | — |
| 403 | `entitlement_exceeded` | `key`, `limit` |
| 403 | `csrf` | — |
| 400 | `parent_version_required` | — |
| 409 | `stale_version` | `current_version` |

`stale_version` has the identical shape across config, platform and
screener settings, which is what lets one `StaleVersionNotice` serve all
of them.

## 9. Freshness fields the console may label

- `/api/v1/system/health`: `feed.frames/reconnects/api_errors/resyncs/
  seq_gaps/rate_limited/msgs_per_sec/latency_ms`, `books[].age_ms`,
  `clock.offset_ms/healthy/last_error`.
- `/api/v1/screener/status`: per-venue `last_poll_at`, `poll_ms`,
  `polls`, `restarts`, `error`; top-level `poll_interval_s`, `updated_at`.
- `/api/v1/screener/auto-paper`: `MarkAgeMs` on positions and drift rows.

## 10. What would need backend work (not done, not requested)

1. A pause spanning the triangular engine **and** Scanner Suite/auto-paper.
2. A reason breakdown for the two pre-gate exit stages.

Explicitly **not** needed, because they compose from existing data: the
auto-paper rule name, and an attention-items count.
