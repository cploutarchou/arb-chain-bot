"use client";

// Overview — priority first (T-087).
//
// What the audit found (docs/design/client-area-audit-2026-09-12 §1):
// twelve status cards, then six session-counter cards, then further
// current-state and exchange-health cards, with operational counters
// given the same visual weight as the user's own results and a drawdown
// figure visibly truncated. Functional, but the questions a person
// actually arrives with cost a scroll and a scan.
//
// The order here answers them in the order they are asked:
//
//   1. Is the platform connected, and is simulation running or paused?
//      → one StatusStrip, replacing twelve cards.
//   2. What needs my attention, and what can I safely do next?
//      → AttentionList, composed from the alert, breaker, clock, feed,
//        ledger-invariant and queue-drop signals, every item carrying
//        its own next step. Restart-pending is excluded on purpose: the
//        shell renders it as a banner above every page already.
//   3. What are my simulated results?
//      → per-asset results at bounded precision, USDC and USDT kept
//        separate, realized and marked kept distinct.
//   4. Why is nothing qualifying, and where do I look next?
//      → the real four-way stage split plus the routes that explain it.
//
// Frames, resyncs, sequence gaps, internal counters and balances move
// into labelled collapsed sections below. Observability is retained, not
// removed: one click away and named, never deleted.
//
// Each section still polls independently, so one degraded endpoint never
// blanks the rest, and a failed cell renders as "unavailable" rather
// than as a zero.

import Link from "next/link";
import { api, type PnLAssetRow, type ScannerStatus } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { worstVerdict } from "@/lib/campaignVerdict";
import { feedState } from "@/lib/feedState";
import { presentDecimal, presentSignedQuote } from "@/lib/decimal";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  Await,
  Badge,
  Collapsible,
  DecimalValue,
  PageTitle,
  Section,
  Stat,
  Table,
  fmtTime,
} from "@/components/ui";
import {
  AttentionList,
  StatusStrip,
  type AttentionItem,
  type StatusSegment,
} from "@/components/StatusStrip";

// FirstRunBanner (console-v2.md §5 trigger: "the first time no
// screener_settings version exists yet" / no rules and no paper balances
// configured) — a dismissible-by-navigation nudge toward /onboarding,
// never a forced redirect (a wizard that traps every login the moment a
// tenant has zero rules would break returning operators just as much as
// first-time ones).
function FirstRunBanner() {
  const rules = usePoll(() => api.screener.rules.list(), 60000);
  const settings = usePoll(() => api.screener.settings.current(), 60000);
  if (rules.kind !== "ready" || settings.kind !== "ready") return null;
  if (rules.data.length > 0) return null;
  const hasBalances = Object.values(settings.data.settings.paper.balances).some(
    (byAsset) => Object.keys(byAsset).length > 0,
  );
  if (hasBalances) return null;
  return (
    <div className="mb-4 flex flex-wrap items-center gap-3 rounded border border-[var(--accent)] bg-[var(--bg-panel)] px-3 py-2 text-[13px]">
      <span className="text-[var(--text)]">
        No venues, balances or rules are configured yet.
      </span>
      <Link
        href="/onboarding"
        className="font-medium text-[var(--accent)] underline"
      >
        Run the setup wizard →
      </Link>
    </div>
  );
}

// ResultRow renders one start asset's results. Separate rows per asset,
// never a cross-asset total: USDC and USDT are different monies and the
// backend deliberately does not sum them either
// (internal/app/readmodel.go returns one row per start asset).
function ResultRow({ row }: { row: PnLAssetRow }) {
  return (
    <div className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3">
      <div className="mb-2 flex items-baseline justify-between gap-2">
        <span className="text-[13px] font-semibold text-[var(--text)]">
          {row.asset}
        </span>
        <span className="text-[10px] uppercase tracking-wider text-[var(--text-dim)]">
          this session
        </span>
      </div>
      <dl className="space-y-1.5 text-[12px]">
        <div className="flex items-baseline justify-between gap-3">
          <dt className="text-[var(--text-dim)]">Realized</dt>
          <dd>
            <DecimalValue
              d={presentSignedQuote(row.realized, row.asset)}
              tone="sign"
            />
          </dd>
        </div>
        <div className="flex items-baseline justify-between gap-3">
          <dt className="text-[var(--text-dim)]">
            Marked exposure
            {row.unmarked.length > 0 && (
              <span
                className="ml-1 text-[var(--warn)]"
                title={`Not valued: ${row.unmarked.join(", ")}`}
              >
                (incomplete)
              </span>
            )}
          </dt>
          <dd>
            <DecimalValue
              d={presentSignedQuote(row.exposure_mark, row.asset)}
              tone="sign"
            />
          </dd>
        </div>
        <div className="flex items-baseline justify-between gap-3 border-t border-[var(--border)] pt-1.5">
          <dt className="font-medium text-[var(--text)]">Net</dt>
          <dd>
            <DecimalValue
              d={presentSignedQuote(row.net_pnl, row.asset)}
              tone="sign"
            />
          </dd>
        </div>
        <div className="flex items-baseline justify-between gap-3">
          <dt className="text-[var(--text-dim)]">Drawdown (peak to trough)</dt>
          <dd>
            <DecimalValue d={presentSignedQuote(row.drawdown, row.asset)} />
          </dd>
        </div>
        <div className="flex items-baseline justify-between gap-3">
          <dt className="text-[var(--text-dim)]">Fees paid</dt>
          <dd>
            <DecimalValue d={presentSignedQuote(row.fees_marked, row.asset)} />
          </dd>
        </div>
      </dl>
    </div>
  );
}

// PipelineExplainer answers "why is nothing qualifying" using only what
// the backend actually reports.
//
// The counters are the same interval but NOT the same stage, and the
// tested scanner invariant is
//
//     evaluations = skipped_unhealthy + no_viable_size + qualified + rejected
//
// so `detected − rejected` is emphatically not `qualified`
// (internal/scanner/scanner.go:338-433 — SkippedBooks and NoViableSize
// both exit before the risk gate is ever reached). Every number below is
// read straight from /scanner/status; nothing is derived, and no
// rejection cause is invented.
//
// A reason breakdown exists for exactly one of these stages: the risk
// gate publishes `reject_reason_counts` on /api/v1/risk. The two
// pre-gate stages have counts and no reason dimension anywhere, which is
// said plainly rather than glossed over.
function PipelineExplainer({ s }: { s: ScannerStatus }) {
  const stages = [
    {
      key: "skipped",
      label: "Skipped — book missing or unhealthy",
      value: s.skipped_unhealthy,
      note: "Never reached the risk gate. No reason breakdown is recorded for this stage.",
      href: "/system",
      action: "Check feed health",
    },
    {
      key: "no-size",
      label: "No viable size",
      value: s.no_viable_size,
      note: "Every candidate size fell below the dust or minimum-notional floor. Counted only; no reason breakdown exists.",
      href: "/settings#markets",
      action: "Review markets & assets",
    },
    {
      key: "rejected",
      label: "Rejected at the risk gate",
      value: s.rejected,
      note: "These have a recorded reason for each rejection.",
      href: "/risk",
      action: "See rejection reasons",
    },
    {
      key: "qualified",
      label: "Qualified",
      value: s.qualified,
      note: "Passed every gate and were handed to the paper engine.",
      href: "/paper",
      action: "Open simulations",
    },
  ];
  const accounted =
    s.skipped_unhealthy + s.no_viable_size + s.rejected + s.qualified;
  return (
    <div>
      <p className="mb-2 max-w-3xl text-[13px] text-[var(--text-dim)]">
        {s.qualified === 0 ? (
          <>
            <strong className="text-[var(--text)]">
              Nothing has qualified in this session yet.
            </strong>{" "}
            Of{" "}
            <DecimalValue d={presentDecimal(s.evaluations, { maxFrac: 0 })} />{" "}
            triangle evaluations, here is where each one stopped. These are four
            distinct stages, not one figure split up — a triangle that exits
            early never reaches the later stages at all.
          </>
        ) : (
          <>
            Of{" "}
            <DecimalValue d={presentDecimal(s.evaluations, { maxFrac: 0 })} />{" "}
            triangle evaluations this session, here is where each one stopped.
          </>
        )}
      </p>
      <ul className="grid max-w-4xl grid-cols-1 gap-2 sm:grid-cols-2">
        {stages.map((st) => (
          <li
            key={st.key}
            className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-2.5"
          >
            <div className="flex items-baseline justify-between gap-2">
              <span className="text-[12px] text-[var(--text-dim)]">
                {st.label}
              </span>
              <DecimalValue d={presentDecimal(st.value, { maxFrac: 0 })} />
            </div>
            <p className="mt-1 text-[11px] leading-snug text-[var(--text-dim)]">
              {st.note}
            </p>
            <Link
              href={st.href}
              className="mt-1 inline-block text-[11px] font-medium text-[var(--accent)] underline"
            >
              {st.action} →
            </Link>
          </li>
        ))}
      </ul>
      {/* The invariant is stated, and so is any gap in it. A mismatch is
          a real signal (events dropped before the consumer saw them),
          not something to hide by adjusting a number. */}
      {accounted !== s.evaluations && (
        <p className="mt-2 max-w-3xl text-[11px] text-[var(--text-dim)]">
          The four stages account for{" "}
          <DecimalValue d={presentDecimal(accounted, { maxFrac: 0 })} /> of{" "}
          <DecimalValue d={presentDecimal(s.evaluations, { maxFrac: 0 })} />{" "}
          evaluations. The difference is events that never reached the consumer
          (
          <DecimalValue d={presentDecimal(s.dropped_events, { maxFrac: 0 })} />{" "}
          dropped) or counters advancing between two reads of the same snapshot.
        </p>
      )}
    </div>
  );
}

export default function OverviewPage() {
  const status = usePoll(() => api.system.status(), 10000);
  const scanner = usePoll(() => api.scanner.status(), 5000);
  const recordings = usePoll(() => api.recordings.list(), 5000);
  const activeAlerts = usePoll(() => api.alerts.list("active", 1), 5000);
  const health = usePoll(() => api.system.health(), 5000);
  const risk = usePoll(() => api.risk(), 8000);
  const pnl = usePoll(() => api.pnl(), 8000);
  const runs = usePoll(() => api.campaigns.list(3), 15000);
  const portfolio = usePoll(() => api.portfolio(), 8000);

  // ---- 1. Status strip -------------------------------------------------
  // Six states, replacing twelve cards. Each one reports its own poll's
  // condition: loading is "…", a failed poll is "unavailable" rather
  // than a made-up value, and a profile that genuinely has no such
  // component says so.
  const segments: StatusSegment[] = [
    {
      label: "Mode",
      loading: status.kind === "loading",
      word: status.kind === "ready" ? status.data.mode : null,
      tone: status.kind === "error" ? "bad" : status.kind === "ready" ? "ok" : "unknown",
      detail: status.kind === "error" ? "unavailable" : "no live orders, ever",
    },
    {
      label: "Market feed",
      loading: health.kind === "loading",
      word:
        health.kind === "ready"
          ? feedState(health.data.books, health.data.feed?.rate_limited).label
          : null,
      tone:
        health.kind === "error"
          ? "bad"
          : health.kind === "ready"
            ? (() => {
                const t = feedState(
                  health.data.books,
                  health.data.feed?.rate_limited,
                ).tone;
                // feedState's "dim" means NOT STARTED — that is unknown,
                // not degraded, and must not read as a mild problem.
                if (t === "ok") return "ok";
                if (t === "bad") return "bad";
                if (t === "dim") return "unknown";
                return "warn";
              })()
            : "unknown",
      detail:
        health.kind === "ready"
          ? feedState(health.data.books, health.data.feed?.rate_limited).detail
          : undefined,
      href: "/exchanges",
    },
    {
      label: "Triangular simulation",
      loading: scanner.kind === "loading",
      word:
        scanner.kind === "ready"
          ? scanner.data.paper
            ? scanner.data.paper.running
              ? "RUNNING"
              : "PAUSED"
            : "NOT RUNNING"
          : null,
      tone:
        scanner.kind === "error"
          ? "bad"
          : scanner.kind === "ready"
            ? scanner.data.paper
              ? scanner.data.paper.running
                ? "ok"
                : "warn"
              : "unknown"
            : "unknown",
      detail:
        scanner.kind === "ready" && !scanner.data.paper
          ? `mode is ${status.kind === "ready" ? status.data.mode : "unknown"}`
          : undefined,
      href: "/paper",
    },
    {
      label: "Scanner",
      loading: scanner.kind === "loading",
      word:
        scanner.kind === "ready"
          ? scanner.data.ready
            ? "READY"
            : "NOT READY"
          : null,
      tone:
        scanner.kind === "error"
          ? "bad"
          : scanner.kind === "ready"
            ? scanner.data.ready
              ? "ok"
              : "warn"
            : "unknown",
      href: "/scanner",
    },
    {
      label: "Venue clock",
      loading: health.kind === "loading",
      word:
        health.kind === "ready"
          ? health.data.clock
            ? health.data.clock.healthy
              ? "OK"
              : "UNSAFE"
            : "NOT MONITORED"
          : null,
      tone:
        health.kind === "error"
          ? "bad"
          : health.kind === "ready"
            ? health.data.clock
              ? health.data.clock.healthy
                ? "ok"
                : "bad"
              : "unknown"
            : "unknown",
      detail:
        health.kind === "ready" && health.data.clock
          ? `offset ${health.data.clock.offset_ms} ms`
          : undefined,
      title:
        health.kind === "ready" && health.data.clock?.last_error
          ? health.data.clock.last_error
          : "An unsafe venue clock gates qualification (RISK_CLOCK_UNSAFE).",
    },
    {
      label: "Database",
      loading: recordings.kind === "loading",
      // `persistence` is a boolean: it distinguishes configured from not
      // configured, and nothing else. A configured-but-failing database
      // is indistinguishable here, so this never claims health — the
      // request that actually fails reports its own error.
      word:
        recordings.kind === "ready"
          ? recordings.data.persistence
            ? "CONFIGURED"
            : "NOT CONFIGURED"
          : null,
      tone:
        recordings.kind === "error"
          ? "bad"
          : recordings.kind === "ready"
            ? recordings.data.persistence
              ? "ok"
              : "unknown"
            : "unknown",
      title:
        "Whether persistence is configured for this deployment. It is not a liveness check.",
    },
  ];

  // ---- 2. Attention ----------------------------------------------------
  // No backend field aggregates this (confirmed in the contract review),
  // so it is composed here from signals that each already exist, and
  // every item names where it came from and where to go.
  const attention: AttentionItem[] = [];
  if (activeAlerts.kind === "ready" && activeAlerts.data.active > 0) {
    attention.push({
      id: "alerts",
      text: `${activeAlerts.data.active} unresolved alert${activeAlerts.data.active === 1 ? "" : "s"}.`,
      tone: "bad",
      action: { href: "/alerts", label: "Review alerts" },
    });
  }
  if (risk.kind === "ready") {
    const open = (risk.data.breakers ?? []).filter((b) => b.State === "OPEN");
    if (open.length > 0) {
      attention.push({
        id: "breakers",
        text: `${open.length} circuit breaker${open.length === 1 ? "" : "s"} open — qualification is gated while any is open.`,
        // The backend's own reason text, verbatim.
        detail: open
          .map((b) => `${b.Name} (${b.Scope || "global"}): ${b.Reason}`)
          .join("; "),
        tone: "bad",
        action: { href: "/risk", label: "Open risk centre" },
      });
    }
  }
  // Restart-pending is deliberately NOT repeated here. It lives on
  // api.engine.status() rather than the health view, and the shell's
  // RestartBanner already renders it as a bordered banner directly above
  // this page — on every page. Adding it would mean a second poll of the
  // same endpoint and the same sentence twice on one screen.
  if (health.kind === "ready") {
    if (health.data.clock && !health.data.clock.healthy) {
      attention.push({
        id: "clock",
        text: "A venue clock is unsafe, which gates qualification.",
        detail: health.data.clock.last_error || undefined,
        tone: "bad",
        action: { href: "/system", label: "System health" },
      });
    }
    const feed = feedState(health.data.books, health.data.feed?.rate_limited);
    // "dim" is NOT STARTED — a deployment-profile fact, not an incident
    // to chase; the status strip already reports it.
    if (feed.tone !== "ok" && feed.tone !== "dim") {
      attention.push({
        id: "feed",
        text: `Market feed is ${feed.label}.`,
        detail: feed.detail,
        tone: feed.tone === "bad" ? "bad" : "warn",
        action: { href: "/exchanges", label: "Check venues" },
      });
    }
    const invariants = health.data.paper?.invariant_violations ?? 0;
    if (invariants > 0) {
      attention.push({
        id: "invariants",
        text: `${invariants} ledger invariant violation${invariants === 1 ? "" : "s"} — the paper engine paused itself.`,
        tone: "bad",
        action: { href: "/paper", label: "Open simulations" },
      });
    }
    const dropped = health.data.queues?.paper?.dropped ?? 0;
    if (dropped > 0) {
      attention.push({
        id: "dropped",
        text: `${dropped} qualified opportunit${dropped === 1 ? "y was" : "ies were"} dropped by a full paper queue.`,
        tone: "warn",
        action: { href: "/system", label: "Queue depths" },
      });
    }
  }
  const attentionLoading =
    activeAlerts.kind === "loading" ||
    risk.kind === "loading" ||
    health.kind === "loading";

  return (
    <ConsoleShell>
      <PageTitle>Overview</PageTitle>
      <FirstRunBanner />

      <StatusStrip segments={segments} label="Platform status" />

      <Section
        title={
          attention.length > 0
            ? `Needs attention (${attention.length})`
            : "Needs attention"
        }
      >
        <AttentionList
          items={attention}
          loading={attentionLoading}
          emptyAction={
            <Link
              href="/screener"
              className="font-medium text-[var(--accent)] underline"
            >
              Look for candidates in Discover →
            </Link>
          }
        />
      </Section>

      <Section title="Simulated results">
        <Await state={pnl} what="simulated results">
          {(p) => {
            const rows = p.assets ?? [];
            if (rows.length === 0) {
              return (
                <p className="text-[13px] text-[var(--text-dim)]">
                  No start asset has a simulated result yet. Results appear once
                  the paper engine settles its first cycle —{" "}
                  <Link href="/paper" className="text-[var(--accent)] underline">
                    open simulations
                  </Link>
                  .
                </p>
              );
            }
            return (
              <>
                <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {rows.map((row) => (
                    <ResultRow key={row.asset} row={row} />
                  ))}
                </div>
                <p className="mt-2 max-w-3xl text-[11px] leading-snug text-[var(--text-dim)]">
                  Simulated results for the current session, one card per start
                  asset and never combined — different assets are different
                  monies. Realized is cash basis; marked exposure values what is
                  still held. Figures are shown to 2 decimals; the exact value
                  is in each figure&apos;s tooltip and on{" "}
                  <Link href="/pnl" className="text-[var(--accent)] underline">
                    Results &amp; analytics
                  </Link>
                  , which also has the historical view. This is a simulation and
                  is not evidence that a profitable opportunity exists.
                </p>
              </>
            );
          }}
        </Await>
      </Section>

      <Section title="This session">
        <Await state={scanner} what="session activity">
          {(s) => (
            <>
              <div className="mb-3 grid max-w-3xl grid-cols-2 gap-3 sm:grid-cols-4">
                <Stat
                  label="Active simulations"
                  value={s.paper?.active_simulations ?? "—"}
                />
                <Stat label="Triangles monitored" value={s.triangles} />
                <Stat label="Markets" value={(s.markets ?? []).length} />
                <Stat
                  label="Cycles completed"
                  value={s.paper?.completed ?? "—"}
                />
              </div>
              <PipelineExplainer s={s} />
            </>
          )}
        </Await>
      </Section>

      <Collapsible
        title="Simulated balances"
        note="available and reserved, per asset"
      >
        <Await state={portfolio} what="portfolio balances">
          {(p) => {
            const entries = Object.entries(p.balances);
            const shown = entries.slice(0, 6);
            return (
              <>
                <Table
                  head={["Asset", "Available", "Reserved"]}
                  align={["text", "num", "num"]}
                  label="Simulated balances"
                  rowKeys={shown.map(([asset]) => asset)}
                  empty="balances (see Balances for the full view)"
                  rows={shown.map(([asset, b]) => [
                    asset,
                    <DecimalValue
                      key="a"
                      d={presentDecimal(b.available, {
                        maxFrac: 2,
                        minFrac: 2,
                        unit: asset,
                      })}
                    />,
                    <DecimalValue
                      key="r"
                      d={presentDecimal(b.reserved, {
                        maxFrac: 2,
                        minFrac: 2,
                        unit: asset,
                      })}
                    />,
                  ])}
                />
                {entries.length > shown.length && (
                  <p className="mt-1 text-[11px] text-[var(--text-dim)]">
                    Showing {shown.length} of {entries.length} assets — full view
                    on{" "}
                    <Link href="/portfolio" className="text-[var(--accent)]">
                      Balances
                    </Link>
                    .
                  </p>
                )}
              </>
            );
          }}
        </Await>
      </Collapsible>

      <Collapsible
        title="Feed diagnostics"
        note="frames, reconnects, resyncs, sequence gaps"
      >
        <Await state={health} what="exchange health">
          {(h) =>
            h.feed ? (
              <>
                <div className="grid max-w-4xl grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-5">
                  <Stat label="Frames" value={h.feed.frames} />
                  <Stat
                    label="Reconnects"
                    value={h.feed.reconnects}
                    tone={h.feed.reconnects > 5 ? "warn" : undefined}
                  />
                  <Stat
                    label="REST errors"
                    value={h.feed.api_errors}
                    tone={h.feed.api_errors > 0 ? "warn" : undefined}
                  />
                  <Stat label="Resyncs" value={h.feed.resyncs} />
                  <Stat
                    label="Sequence gaps"
                    value={h.feed.seq_gaps}
                    tone={h.feed.seq_gaps > 0 ? "warn" : undefined}
                  />
                </div>
                <p className="mt-2 text-[11px] text-[var(--text-dim)]">
                  Connection-level counters for the market-data feed. The full
                  view, including per-market book ages, is on{" "}
                  <Link href="/system" className="text-[var(--accent)] underline">
                    System health
                  </Link>
                  .
                </p>
              </>
            ) : (
              <p className="text-sm text-[var(--text-dim)]">
                The feed has not started in this deployment profile — see{" "}
                <Link href="/exchanges" className="text-[var(--accent)] underline">
                  Venues
                </Link>
                .
              </p>
            )
          }
        </Await>
      </Collapsible>

      <Collapsible
        title="Engine internals"
        note="revalidations, queue drops, ledger invariants"
      >
        {health.kind === "ready" && health.data.scanner ? (
          <ul className="space-y-1 text-[12px] text-[var(--text-dim)]">
            <li>
              Revalidations before execution:{" "}
              {health.data.scanner.revalidations ?? 0}
              {health.data.scanner.revalidation_rejects
                ? ` (${health.data.scanner.revalidation_rejects} refused the second check)`
                : ""}
            </li>
            <li>
              Qualified opportunities dropped by a full paper queue:{" "}
              {health.data.queues?.paper?.dropped ?? 0}
            </li>
            <li>
              Ledger invariant violations:{" "}
              {health.data.paper?.invariant_violations ?? 0}
              {health.data.paper?.invariant_violations
                ? " — the engine pauses itself when this is non-zero"
                : ""}
            </li>
            <li>
              Events dropped before the consumer read them:{" "}
              {scanner.kind === "ready" ? scanner.data.dropped_events : "—"}
            </li>
          </ul>
        ) : (
          <p className="text-[12px] text-[var(--text-dim)]">
            {health.kind === "error"
              ? "System health is unavailable right now, so these counters cannot be read."
              : health.kind === "loading"
                ? "Loading…"
                : "This deployment profile reports no scanner internals."}
          </p>
        )}
      </Collapsible>

      <Section title="Recent campaign verdicts">
        <Await state={runs} what="campaign runs">
          {(r) => {
            const list = r.runs ?? [];
            if (list.length === 0) {
              return (
                <p className="text-sm text-[var(--text-dim)]">
                  No campaign runs yet. A campaign is how a strategy gets a
                  recorded verdict —{" "}
                  <Link href="/campaigns" className="text-[var(--accent)] underline">
                    open Campaigns
                  </Link>
                  .
                </p>
              );
            }
            return (
              <Table
                head={["Run", "Recording", "Status", "Verdict", "Finished"]}
                empty="campaign runs"
                label="Recent campaign verdicts"
                rowKeys={list.map((run) => run.id)}
                rows={list.map((run) => {
                  const v = worstVerdict(run);
                  return [
                    <span key="id" className="text-[var(--text-dim)]">
                      {run.id}
                    </span>,
                    run.recording,
                    <Badge
                      key="s"
                      tone={
                        run.status === "done"
                          ? "ok"
                          : run.status === "failed"
                            ? "bad"
                            : run.status === "running"
                              ? "warn"
                              : "dim"
                      }
                    >
                      {run.status}
                    </Badge>,
                    v ? (
                      <div
                        key="v"
                        className="max-w-sm whitespace-normal break-words"
                      >
                        <Badge tone={v.tone}>{v.text}</Badge>
                      </div>
                    ) : (
                      <span key="v" className="text-[var(--text-dim)]">
                        — pending —
                      </span>
                    ),
                    run.finished_at ? fmtTime(run.finished_at) : "—",
                  ];
                })}
              />
            );
          }}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
