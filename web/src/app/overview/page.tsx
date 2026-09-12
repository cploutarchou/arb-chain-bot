"use client";

// Real operations dashboard (console-ux-audit.md §2.1, BL-01/BL-25) that
// passes the five-second test (audit F5): in one screen an operator
// sees the mode, whether the market is visible (feed state, clock), the
// engines (scanner, paper with a reason when absent, recorder), the
// money (session realized PnL, drawdown, fees per start asset), the
// risk (breakers open, unresolved alerts) and capital in use — then the
// detail sections. Assembled from endpoints that already exist; each
// section polls independently so one degraded endpoint never blanks the
// rest, and per-cell errors render as small "unavailable" stats instead
// of six stacked page-level failures.

import type { ReactNode } from "react";
import Link from "next/link";
import { api } from "@/lib/api/client";
import { usePoll, type PollState } from "@/lib/usePoll";
import { worstVerdict } from "@/lib/campaignVerdict";
import { feedState } from "@/lib/feedState";
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

// statOrError renders one status-strip cell without the shared kit's full
// bordered ErrorBox — many of those stacked in a grid (one per upstream
// endpoint) would read as page-level failures instead of small stats,
// one of which happens to be unavailable right now.
function statOrError<T>(
  state: PollState<T>,
  label: string,
  pick: (data: T) => { value: ReactNode; tone?: Tone; href?: string; title?: string },
): ReactNode {
  let stat: ReactNode;
  if (state.kind === "loading") {
    stat = <Stat label={label} value="…" />;
  } else if (state.kind === "error") {
    stat = <Stat label={label} value="unavailable" tone="bad" />;
  } else {
    const { value, tone, href, title } = pick(state.data);
    const el = (
      <Stat label={label} value={<span title={title}>{value}</span>} tone={tone} />
    );
    stat = href ? <Link href={href}>{el}</Link> : el;
  }
  return <div key={label}>{stat}</div>;
}

// perAsset renders "value asset" per start asset joined on one line —
// the PnL view's own per-asset decimal strings, verbatim, never summed
// (different assets are different monies).
function perAsset<T extends { asset: string }>(
  rows: T[],
  get: (row: T) => string | undefined,
): string {
  const parts = rows
    .map((r) => ({ asset: r.asset, v: get(r) ?? "" }))
    .filter((r) => r.v !== "");
  if (parts.length === 0) return "—";
  return parts.map((p) => `${p.v} ${p.asset}`).join(" · ");
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

  return (
    <ConsoleShell>
      <PageTitle>Overview</PageTitle>
      <FirstRunBanner />

      <Section title="Status">
        <div className="grid max-w-5xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-6">
          {statOrError(status, "Mode", (s) => ({ value: s.mode }))}
          {statOrError(health, "Feed", (h) => {
            const f = feedState(h.books, h.feed?.rate_limited);
            return { value: f.label, tone: f.tone, title: f.detail, href: "/exchanges" };
          })}
          {statOrError(health, "Venue clock", (h) =>
            h.clock
              ? {
                  value: h.clock.healthy ? "OK" : "UNSAFE",
                  tone: h.clock.healthy ? "ok" : "bad",
                  title: `offset ${h.clock.offset_ms} ms${h.clock.last_error ? `; last error: ${h.clock.last_error}` : ""} — RISK_CLOCK_UNSAFE gates qualification while unhealthy`,
                }
              : { value: "N/A", tone: "dim", title: "no clock monitor in this profile" },
          )}
          {statOrError(scanner, "Scanner", (s) => ({
            value: s.ready ? "READY" : "NOT READY",
            tone: s.ready ? "ok" : "warn",
          }))}
          {statOrError(scanner, "Paper engine", (s) =>
            s.paper
              ? {
                  value: s.paper.running ? "RUNNING" : "PAUSED",
                  tone: s.paper.running ? "ok" : "warn",
                  href: "/paper",
                }
              : {
                  value: "NOT RUNNING",
                  tone: "dim",
                  title: `not running — mode is ${mode ?? "unknown"}`,
                },
          )}
          {statOrError(recordings, "Recorder", (r) =>
            r.recorder
              ? {
                  value: r.recorder.running ? "RECORDING" : "IDLE",
                  tone: r.recorder.running ? "ok" : "warn",
                }
              : { value: "N/A", tone: "dim", title: "no recorder in this profile" },
          )}
          {statOrError(pnl, "Realized PnL (session)", (p) => {
            const rows = p.assets ?? [];
            const first = rows[0];
            return {
              value: perAsset(rows, (r) => r.realized),
              tone: first ? (first.realized.trim().startsWith("-") ? "bad" : "ok") : "dim",
              title: "cash basis per start asset — marked exposure is separate (see PnL & Analytics)",
              href: "/pnl",
            };
          })}
          {statOrError(pnl, "Drawdown", (p) => ({
            value: perAsset(p.assets ?? [], (r) => r.drawdown),
            tone: "dim",
            title: "peak-to-trough per start asset; compare against max_drawdown in the Risk Center",
            href: "/portfolio",
          }))}
          {statOrError(pnl, "Fees paid (valued)", (p) => ({
            value: perAsset(p.assets ?? [], (r) => r.fees_marked),
            tone: "dim",
            title: "every fee asset valued in the start asset — the honest bill (fees in the start asset alone: per-asset table on /pnl)",
            href: "/pnl",
          }))}
          {statOrError(risk, "Breakers open", (r) => {
            const open = (r.breakers ?? []).filter((b) => b.State === "OPEN");
            return {
              value: open.length,
              tone: open.length > 0 ? "bad" : "ok",
              title: open.length > 0 ? open.map((b) => `${b.Name} (${b.Scope || "global"}): ${b.Reason}`).join("; ") : "no breaker is open",
              href: "/risk",
            };
          })}
          {statOrError(activeAlerts, "Unresolved alerts", (a) => ({
            value: a.active,
            tone: a.active > 0 ? "bad" : "ok",
            href: "/alerts",
          }))}
          {statOrError(recordings, "Database", (r) => ({
            value: r.persistence ? "CONNECTED" : "NOT CONFIGURED",
            tone: r.persistence ? "ok" : "dim",
          }))}
        </div>
      </Section>

      <Section title="Today (session counters)">
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

      <Section title="Current">
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
                    b.available,
                    b.reserved,
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

      <Section title="Exchange health">
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
