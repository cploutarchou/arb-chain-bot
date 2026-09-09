"use client";

// Real operations dashboard (console-ux-audit.md §2.1, BL-01/BL-25): a
// status strip, today's/current stats, exchange health, recent campaign
// verdicts, and quick-action links — assembled entirely from endpoints
// that already exist. On the shared kit throughout (usePoll/Await/Stat),
// and each section polls independently so one degraded endpoint never
// blanks the rest of the dashboard.

import type { ReactNode } from "react";
import Link from "next/link";
import { api } from "@/lib/api/client";
import { usePoll, type PollState } from "@/lib/usePoll";
import { worstVerdict } from "@/lib/campaignVerdict";
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
// bordered ErrorBox — six of those stacked in a grid (one per upstream
// endpoint) would read as six page-level failures instead of six small
// stats, one of which happens to be unavailable right now.
function statOrError<T>(
  state: PollState<T>,
  label: string,
  pick: (data: T) => { value: ReactNode; tone?: Tone; href?: string },
): ReactNode {
  let stat: ReactNode;
  if (state.kind === "loading") {
    stat = <Stat label={label} value="…" />;
  } else if (state.kind === "error") {
    stat = <Stat label={label} value="unavailable" tone="bad" />;
  } else {
    const { value, tone, href } = pick(state.data);
    const el = <Stat label={label} value={value} tone={tone} />;
    stat = href ? <Link href={href}>{el}</Link> : el;
  }
  return <div key={label}>{stat}</div>;
}

export default function OverviewPage() {
  const status = usePoll(() => api.system.status(), 10000);
  const scanner = usePoll(() => api.scanner.status(), 5000);
  const recordings = usePoll(() => api.recordings.list(), 5000);
  const activeAlerts = usePoll(() => api.alerts.list("active", 1), 5000);
  const health = usePoll(() => api.system.health(), 5000);
  const runs = usePoll(() => api.campaigns.list(3), 15000);
  const portfolio = usePoll(() => api.portfolio(), 8000);

  return (
    <ConsoleShell active="Overview">
      <PageTitle>Overview</PageTitle>
      <FirstRunBanner />

      <Section title="Status">
        <div className="grid max-w-5xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-6">
          {statOrError(status, "Mode", (s) => ({ value: s.mode }))}
          {statOrError(scanner, "Scanner", (s) => ({
            value: s.ready ? "READY" : "NOT READY",
            tone: s.ready ? "ok" : "warn",
          }))}
          {statOrError(scanner, "Paper engine", (s) =>
            s.paper
              ? {
                  value: s.paper.running ? "RUNNING" : "PAUSED",
                  tone: s.paper.running ? "ok" : "warn",
                }
              : { value: "N/A" },
          )}
          {statOrError(recordings, "Recorder", (r) =>
            r.recorder
              ? {
                  value: r.recorder.running ? "RECORDING" : "IDLE",
                  tone: r.recorder.running ? "ok" : "warn",
                }
              : { value: "N/A" },
          )}
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
      </Section>

      <Section title="Current">
        <Await state={scanner} what="active simulations">
          {(s) => (
            <div className="mb-3 grid max-w-2xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-3">
              <Stat
                label="Active simulations"
                value={s.paper?.active_simulations ?? "—"}
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
            return (
              <>
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
