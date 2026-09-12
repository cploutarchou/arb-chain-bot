"use client";

// Real operations dashboard, priority-first (client-area audit
// 2026-09-12 §1/§4B): attention items above everything, then a compact
// platform status strip and the session's primary results — the money
// (realized PnL, drawdown, fees per start asset), the active work —
// with diagnostics (session counters, feed internals, balances)
// clearly labelled below. Zero qualified candidates is explained from
// the engine's own rejection-reason histogram, never inferred from
// detected − rejected. Assembled from endpoints that already exist;
// each section polls independently so one degraded endpoint never
// blanks the rest, and per-cell errors render as small "unavailable"
// cells instead of six stacked page-level failures.

import type { ReactNode } from "react";
import Link from "next/link";
import { api } from "@/lib/api/client";
import { usePoll, type PollState } from "@/lib/usePoll";
import { worstVerdict } from "@/lib/campaignVerdict";
import { feedState } from "@/lib/feedState";
import { fmtDecimal } from "@/lib/decimal";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  Await,
  Badge,
  PageTitle,
  Section,
  Stat,
  Table,
  fmtTime,
  type Tone,
} from "@/components/ui";

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

// AttentionItem is one "this needs you" line: tone, the fact, and where
// to act. Only real states produce items — loading never does (a
// half-loaded page must not cry wolf), and errors surface through their
// own sections rather than as fake attention.
interface AttentionItem {
  tone: "bad" | "warn";
  text: string;
  href: string;
  link: string;
}

function AttentionRow({ item }: { item: AttentionItem }) {
  return (
    <li className="flex flex-wrap items-baseline gap-2">
      <span
        className={`inline-block h-2 w-2 shrink-0 translate-y-[-1px] rounded-full ${
          item.tone === "bad"
            ? "bg-[var(--critical)]"
            : "bg-[var(--warn)]"
        }`}
        aria-hidden
      />
      <span
        className={
          item.tone === "bad"
            ? "text-[var(--critical)]"
            : "text-[var(--warn)]"
        }
      >
        {item.text}
      </span>
      <Link
        href={item.href}
        className="text-[12px] text-[var(--accent)] underline"
      >
        {item.link} →
      </Link>
    </li>
  );
}

// statCell renders one status-strip cell without the shared kit's full
// bordered ErrorBox — many of those stacked in a grid (one per upstream
// endpoint) would read as page-level failures instead of small cells,
// one of which happens to be unavailable right now.
function statCell<T>(
  state: PollState<T>,
  label: string,
  pick: (data: T) => { value: ReactNode; tone?: Tone; title?: string },
): ReactNode {
  if (state.kind === "loading") {
    return (
      <div key={label} className="min-w-0">
        <div className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">
          {label}
        </div>
        <div className="text-[13px] text-[var(--text-dim)]">…</div>
      </div>
    );
  }
  if (state.kind === "error") {
    return (
      <div key={label} className="min-w-0">
        <div className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">
          {label}
        </div>
        <div className="text-[13px] text-[var(--critical)]">unavailable</div>
      </div>
    );
  }
  const { value, tone, title } = pick(state.data);
  const color =
    tone === "ok"
      ? "text-[var(--ok)]"
      : tone === "warn"
        ? "text-[var(--warn)]"
        : tone === "bad"
          ? "text-[var(--critical)]"
          : "text-[var(--text)]";
  return (
    <div key={label} className="min-w-0" title={title}>
      <div className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">
        {label}
      </div>
      <div className={`truncate text-[13px] font-medium ${color}`}>{value}</div>
    </div>
  );
}

// perAsset renders "value asset" per start asset joined on one line —
// the PnL view's own per-asset decimal strings, never summed (different
// assets are different monies). Display goes through fmtDecimal with
// the raw line as the tooltip (the audit's truncated-drawdown finding).
function perAsset<T extends { asset: string }>(
  rows: T[],
  get: (row: T) => string | undefined,
): { text: string; raw: string } {
  const parts = rows
    .map((r) => ({ asset: r.asset, v: get(r) ?? "" }))
    .filter((r) => r.v !== "");
  if (parts.length === 0) return { text: "—", raw: "no per-asset rows" };
  return {
    text: parts.map((p) => `${fmtDecimal(p.v)} ${p.asset}`).join(" · "),
    raw: parts.map((p) => `${p.v} ${p.asset}`).join(" · "),
  };
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

  const mode = status.kind === "ready" ? status.data.mode : undefined;

  // ---- Attention: what needs the operator, computed from the same
  // polls the strip uses. Loading/absent data never fabricates items.
  const attention: AttentionItem[] = [];
  if (risk.kind === "ready") {
    const open = (risk.data.breakers ?? []).filter((b) => b.State === "OPEN");
    if (open.length > 0) {
      attention.push({
        tone: "bad",
        text: `${open.length} risk breaker${open.length > 1 ? "s" : ""} open — qualification is gated (${open
          .map((b) => b.Name)
          .join(", ")}).`,
        href: "/risk",
        link: "Risk Center",
      });
    }
  }
  if (activeAlerts.kind === "ready" && activeAlerts.data.active > 0) {
    attention.push({
      tone: "bad",
      text: `${activeAlerts.data.active} unresolved alert${activeAlerts.data.active > 1 ? "s" : ""}.`,
      href: "/alerts",
      link: "Alerts",
    });
  }
  if (health.kind === "ready" && health.data.clock && !health.data.clock.healthy) {
    attention.push({
      tone: "bad",
      text: "Venue clock unsafe — qualification is gated.",
      href: "/exchanges",
      link: "Exchanges",
    });
  }
  if (health.kind === "ready" && health.data.feed?.rate_limited) {
    attention.push({
      tone: "warn",
      text: "Feed is rate-limited — books may go stale.",
      href: "/exchanges",
      link: "Exchanges",
    });
  }
  if (scanner.kind === "ready" && scanner.data.paper && !scanner.data.paper.running) {
    attention.push({
      tone: "warn",
      text: "Paper simulation is paused.",
      href: "/paper",
      link: "Paper Trading",
    });
  }
  if (recordings.kind === "ready" && !recordings.data.persistence) {
    attention.push({
      tone: "warn",
      text: "Persistence not configured — results live in memory only and are lost on restart.",
      href: "/system",
      link: "System Health",
    });
  }

  return (
    <ConsoleShell>
      <PageTitle>Overview</PageTitle>
      <FirstRunBanner />

      {/* Attention first (audit §4B): what needs the operator, above the
          fold, each line linking to where it is acted on. */}
      <Section title="Needs attention">
        {attention.length === 0 ? (
          <p className="text-[13px] text-[var(--ok)]">
            Nothing needs attention.{" "}
            <Link href="/screener" className="text-[var(--accent)] underline">
              Scan for candidates
            </Link>{" "}
            or{" "}
            <Link href="/paper" className="text-[var(--accent)] underline">
              open Paper Trading
            </Link>
            .
          </p>
        ) : (
          <ul className="space-y-1.5 text-[13px]">
            {attention.map((item, i) => (
              <AttentionRow key={i} item={item} />
            ))}
          </ul>
        )}
      </Section>

      {/* The platform strip (audit §4B): connection, mode and engine
          states as one readable row — the "is it connected and running"
          answer — with the money and risk states promoted out of the old
          12-card grid into the sections that own them. */}
      <Section title="Platform">
        <div className="grid max-w-5xl grid-cols-3 gap-x-4 gap-y-3 sm:grid-cols-4 md:grid-cols-7">
          {statCell(status, "Mode", (s) => ({ value: s.mode }))}
          {statCell(health, "Feed", (h) => {
            const f = feedState(h.books, h.feed?.rate_limited);
            return { value: f.label, tone: f.tone, title: f.detail };
          })}
          {statCell(health, "Venue clock", (h) =>
            h.clock
              ? {
                  value: h.clock.healthy ? "OK" : "UNSAFE",
                  tone: h.clock.healthy ? "ok" : "bad",
                  title: `offset ${h.clock.offset_ms} ms${h.clock.last_error ? `; last error: ${h.clock.last_error}` : ""} — RISK_CLOCK_UNSAFE gates qualification while unhealthy`,
                }
              : { value: "N/A", tone: "dim", title: "no clock monitor in this profile" },
          )}
          {statCell(scanner, "Scanner", (s) => ({
            value: s.ready ? "READY" : "NOT READY",
            tone: s.ready ? "ok" : "warn",
          }))}
          {statCell(scanner, "Paper engine", (s) =>
            s.paper
              ? {
                  value: s.paper.running ? "RUNNING" : "PAUSED",
                  tone: s.paper.running ? "ok" : "warn",
                }
              : {
                  value: "NOT RUNNING",
                  tone: "dim",
                  title: `not running — mode is ${mode ?? "unknown"}`,
                },
          )}
          {statCell(recordings, "Recorder", (r) =>
            r.recorder
              ? {
                  value: r.recorder.running ? "RECORDING" : "IDLE",
                  tone: r.recorder.running ? "ok" : "dim",
                }
              : { value: "N/A", tone: "dim", title: "no recorder in this profile" },
          )}
          {statCell(recordings, "Database", (r) => ({
            value: r.persistence ? "CONNECTED" : "NOT CONFIGURED",
            tone: r.persistence ? "ok" : "warn",
          }))}
        </div>
      </Section>

      {/* The session's primary summaries (audit §4B): simulated net
          results, drawdown, active work — fees explicit, per asset,
          never summed across currencies. */}
      <Section title="Results (session)">
        <div className="grid max-w-5xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
          {(() => {
            const realized = perAsset(pnl.kind === "ready" ? (pnl.data.assets ?? []) : [], (r) => r.realized);
            const first = pnl.kind === "ready" ? (pnl.data.assets ?? [])[0] : undefined;
            return (
              <Stat
                label="Realized PnL"
                value={realized.text}
                exact={realized.raw}
                tone={first ? (first.realized.trim().startsWith("-") ? "bad" : "ok") : "dim"}
              />
            );
          })()}
          {(() => {
            const dd = perAsset(pnl.kind === "ready" ? (pnl.data.assets ?? []) : [], (r) => r.drawdown);
            return (
              <Stat
                label="Drawdown"
                value={dd.text}
                exact={dd.raw}
                tone="dim"
              />
            );
          })()}
          {(() => {
            const fees = perAsset(pnl.kind === "ready" ? (pnl.data.assets ?? []) : [], (r) => r.fees_marked);
            return (
              <Stat
                label="Fees paid (valued)"
                value={fees.text}
                exact={fees.raw}
                tone="dim"
              />
            );
          })()}
          <Await state={scanner} what="active work">
            {(s) => (
              <Stat
                label="Active work"
                value={
                  s.paper
                    ? `${s.paper.active_simulations} simulation${s.paper.active_simulations === 1 ? "" : "s"} in flight`
                    : "engine not running"
                }
                tone={s.paper?.running ? "ok" : "dim"}
              />
            )}
          </Await>
        </div>
        {/* Zero qualified candidates is explained from the engine's own
            rejection histogram — never inferred (audit §4B: detected −
            rejected is not qualified; counters cover different stages). */}
        <Await state={scanner} what="qualified count">
          {(s) => {
            if (s.qualified > 0) {
              return (
                <p className="mt-2 text-[12px] text-[var(--text-dim)]">
                  {s.qualified} qualified of {s.evaluations} evaluated today.
                </p>
              );
            }
            if (s.evaluations === 0) {
              return (
                <p className="mt-2 text-[12px] text-[var(--text-dim)]">
                  Nothing evaluated yet — the scanner needs healthy books.{" "}
                  <Link href="/exchanges" className="text-[var(--accent)] underline">
                    Exchange health
                  </Link>
                </p>
              );
            }
            const counts =
              risk.kind === "ready" ? risk.data.reject_reason_counts ?? {} : {};
            const top = Object.entries(counts)
              .sort((a, b) => b[1] - a[1])
              .slice(0, 3);
            return (
              <p className="mt-2 text-[12px] text-[var(--text-dim)]">
                Nothing qualified today ({s.evaluations} evaluated
                {top.length > 0
                  ? `; the engine's top rejection reasons since start: ${top
                      .map(([reason, n]) => `${reason} ×${n}`)
                      .join(", ")}`
                  : ""}
                ).{" "}
                <Link href="/scanner" className="text-[var(--accent)] underline">
                  Scanner detail
                </Link>
              </p>
            );
          }}
        </Await>
      </Section>

      <Section title="Session counters (diagnostics)">
        <Await state={scanner} what="today's counters">
          {(s) => (
            <div className="grid max-w-5xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-6">
              <Stat label="Opportunities detected" value={s.evaluations} />
              <Stat label="Qualified" value={s.qualified} tone="ok" />
              <Stat label="Rejected" value={s.rejected} />
              <Stat
                label="Paper cycles received"
                value={s.paper?.received ?? "—"}
              />
              <Stat
                label="Paper cycles completed"
                value={s.paper?.completed ?? "—"}
                tone="ok"
              />
              <Stat
                label="Paper cycles failed"
                value={s.paper?.failed ?? "—"}
                tone={s.paper && s.paper.failed > 0 ? "warn" : undefined}
              />
            </div>
          )}
        </Await>
        {health.kind === "ready" && health.data.scanner && (
          <p className="mt-2 text-[11px] text-[var(--text-dim)]">
            Revalidations before execution: {health.data.scanner.revalidations ?? 0}
            {health.data.scanner.revalidation_rejects
              ? ` (${health.data.scanner.revalidation_rejects} refused the second check)`
              : ""}
            {health.data.queues?.paper?.dropped
              ? ` · ${health.data.queues.paper.dropped} qualified opportunities dropped by a full paper queue`
              : ""}
            {health.data.paper?.invariant_violations
              ? ` · ${health.data.paper.invariant_violations} ledger invariant violations (engine paused)`
              : ""}
          </p>
        )}
      </Section>

      <Section title="Capital & balances">
        <Await state={scanner} what="active simulations">
          {(s) => (
            <div className="mb-3 grid max-w-2xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
              <Stat
                label="Active simulations"
                value={s.paper?.active_simulations ?? "—"}
              />
              <Stat
                label="In flight (live view)"
                value={
                  <Link href="/paper" className="text-[var(--accent)] underline">
                    monitor →
                  </Link>
                }
              />
              <Stat label="Triangles" value={s.triangles} />
              <Stat label="Markets" value={(s.markets ?? []).length} />
            </div>
          )}
        </Await>
        <Await state={portfolio} what="portfolio balances">
          {(p) => {
            const entries = Object.entries(p.balances);
            const shown = entries.slice(0, 6);
            // Capital in use: how many start assets currently hold
            // reserved balances. A cross-asset total would sum different
            // monies; the count plus the per-asset rows below is the
            // honest five-second answer.
            const reservedAssets = entries.filter(
              ([, b]) => b.reserved !== "0" && !/^(0\.0+)$/.test(b.reserved),
            ).length;
            return (
              <>
                <div className="mb-3 grid max-w-2xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
                  <Stat
                    label="Capital in use"
                    value={
                      reservedAssets > 0
                        ? `${reservedAssets} of ${entries.length} assets`
                        : "none"
                    }
                    tone={reservedAssets > 0 ? "warn" : "dim"}
                  />
                </div>
                <Table
                  head={["Asset", "Available", "Reserved"]}
                  empty="balances (see Portfolio & Balances for the full view)"
                  rows={shown.map(([asset, b]) => [
                    asset,
                    <span key="a" title={b.available}>
                      {fmtDecimal(b.available)}
                    </span>,
                    <span key="r" title={b.reserved}>
                      {fmtDecimal(b.reserved)}
                    </span>,
                  ])}
                />
                {entries.length > shown.length && (
                  <p className="mt-1 text-[11px] text-[var(--text-dim)]">
                    Showing {shown.length} of {entries.length} assets — full
                    view on{" "}
                    <Link href="/portfolio" className="text-[var(--accent)]">
                      Portfolio &amp; Balances
                    </Link>
                    .
                  </p>
                )}
              </>
            );
          }}
        </Await>
      </Section>

      <Section title="Feed diagnostics">
        <Await state={health} what="exchange health">
          {(h) =>
            h.feed ? (
              <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-5">
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
            ) : (
              <p className="text-sm text-[var(--text-dim)]">
                Feed not started yet — see Exchanges.
              </p>
            )
          }
        </Await>
      </Section>

      <Section title="Recent campaign verdicts">
        <Await state={runs} what="campaign runs">
          {(r) => {
            const list = r.runs ?? [];
            if (list.length === 0) {
              return (
                <p className="text-sm text-[var(--text-dim)]">
                  No campaign runs yet. Launch one from{" "}
                  <Link href="/campaigns" className="text-[var(--accent)]">
                    Campaigns
                  </Link>
                  .
                </p>
              );
            }
            return (
              <Table
                head={["Run", "Recording", "Status", "Verdict", "Finished"]}
                empty="campaign runs"
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

      <Section title="Quick actions">
        <div className="flex flex-wrap gap-2 text-[13px]">
          <QuickLink href="/screener" label="Screener" />
          <QuickLink href="/campaigns" label="Campaigns" />
          <QuickLink href="/paper" label="Paper Trading" />
          <QuickLink href="/alerts" label="Alerts" />
          <QuickLink href="/strategies" label="Strategies" />
        </div>
      </Section>
    </ConsoleShell>
  );
}

function QuickLink({ href, label }: { href: string; label: string }) {
  return (
    <Link
      href={href}
      className="rounded border border-[var(--border)] px-2.5 py-1 text-[var(--text)] hover:bg-[var(--bg-raised)]"
    >
      {label} →
    </Link>
  );
}
