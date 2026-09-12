"use client";

// Screener paper reports (T-078, internal/screener/report,
// docs/user-guide/reports.md "Screener paper reports"): the nightly
// (00:05 UTC) and on-demand evidence reports for the Scanner Suite's
// automatic PAPER execution — GET /screener/reports (screener:view),
// GET /screener/reports/{id} (screener:view, separate page), POST
// /screener/reports/run (screener:config, ADMIN, CSRF, audited). This is
// the console page docs/user-guide/reports.md currently calls "planned".

import { useState } from "react";
import Link from "next/link";
import { api, ApiError, type ScreenerReportSummary } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  HYPOTHETICAL_PERFORMANCE_FOOTER,
  ScreenerAwait,
} from "@/components/screener/ScreenerShared";
import {
  Badge,
  Button,
  ConfirmDialog,
  PageTitle,
  Section,
  Stat,
  Table,
  fmtTime,
  type Tone,
} from "@/components/ui";

function gateTone(passed: number, total: number): Tone {
  if (total === 0) return "dim";
  if (passed === total) return "ok";
  return passed > 0 ? "warn" : "dim";
}

function ruleCell(id: string): string {
  return id || "— (strategy aggregate)";
}

export default function ScreenerReportsPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayRun = can(role, "screener:config");

  const [refresh, setRefresh] = useState(0);
  const list = usePoll(() => api.screener.reports.list(50), 30000, [refresh]);

  const [confirmRun, setConfirmRun] = useState(false);
  const [runBusy, setRunBusy] = useState(false);
  const [runErr, setRunErr] = useState("");
  const [runMsg, setRunMsg] = useState("");

  const confirmedRun = async () => {
    setRunBusy(true);
    setRunErr("");
    try {
      const { run } = await api.screener.reports.run();
      setRunMsg(
        `Ran for ${run.day}: ${run.reports?.length ?? 0} report(s)` +
          (run.errors?.length ? `, ${run.errors.length} error(s)` : "") +
          ".",
      );
      setConfirmRun(false);
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setRunErr(err instanceof ApiError ? err.message : "Run failed.");
    } finally {
      setRunBusy(false);
    }
  };

  return (
    <ConsoleShell>
      <PageTitle>Screener Reports</PageTitle>

      <ScreenerAwait state={list} what="screener reports">
        {(data) => (
          <>
            <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Stat
                label="Next run (UTC)"
                value={data.next_run_utc ? fmtTime(data.next_run_utc) : "—"}
              />
              <Stat
                label="Last run"
                value={data.last_run ? `${data.last_run.day}` : "never"}
              />
              <Stat
                label="Last run reports"
                value={
                  data.last_run ? (data.last_run.reports?.length ?? 0) : "—"
                }
              />
              <Stat
                label="Last run errors"
                value={data.last_run?.errors?.length ?? 0}
                tone={data.last_run?.errors?.length ? "warn" : "dim"}
              />
            </div>

            {mayRun && (
              <div className="mb-4">
                <Button onClick={() => setConfirmRun(true)}>Run now…</Button>
                {runMsg && (
                  <span className="ml-3 text-[12px] text-[var(--ok)]">
                    {runMsg}
                  </span>
                )}
              </div>
            )}

            <Section title="Reports">
              <Table
                head={[
                  "Period",
                  "Window",
                  "Strategy",
                  "Rule",
                  "n",
                  "Net PnL (quote, after fees)",
                  "Hit rate (95% CI)",
                  "Gate",
                  "",
                ]}
                align={[
                  "text",
                  "text",
                  "text",
                  "text",
                  "num",
                  "num",
                  "text",
                  "text",
                  "text",
                ]}
                empty="screener reports — none generated yet"
                sticky
                maxHeight={520}
                rows={(data.reports ?? []).map((r: ScreenerReportSummary) => [
                  <Badge key="p" tone="dim">
                    {r.period_label}
                  </Badge>,
                  `${fmtTime(r.period_start)} – ${fmtTime(r.period_end)}`,
                  r.strategy,
                  ruleCell(r.rule_id),
                  r.n,
                  r.realised_net_pnl_quote ?? r.net_pnl_quote ?? "—",
                  // report.Summary (the list row) does not carry hit_rate
                  // or its Wilson CI — those live only in the Stats block
                  // GET /reports/{id} returns (internal/screener/report/
                  // report.go Summary() vs Payload.Stats). Never fabricate
                  // it here; open the report for the real figure.
                  <span key="hr" className="text-[var(--text-dim)]">
                    see report
                  </span>,
                  <Badge key="g" tone={gateTone(r.gate_passed, r.gate_total)}>
                    {r.gate_passed}/{r.gate_total} pass
                  </Badge>,
                  <Link
                    key="v"
                    href={`/screener-reports/${encodeURIComponent(r.id)}`}
                    className="text-[12px] text-[var(--accent)] underline"
                  >
                    View
                  </Link>,
                ])}
              />
            </Section>
          </>
        )}
      </ScreenerAwait>

      <p className="mt-3 max-w-3xl text-[12px] text-[var(--text-dim)]">
        {HYPOTHETICAL_PERFORMANCE_FOOTER}
      </p>

      {confirmRun && (
        <ConfirmDialog
          title="Run screener reports now?"
          confirmLabel={runBusy ? "Running…" : "Run now"}
          confirmDisabled={runBusy}
          onConfirm={confirmedRun}
          onCancel={() => {
            setConfirmRun(false);
            setRunErr("");
          }}
          body={
            <div>
              <p className="mb-2">
                Computes the previous UTC day and the cumulative window for
                every strategy that has rules or ledger rows, and every rule
                under it, files markdown/JSON (when a recordings directory is
                configured) and stores a report row. This does not place any
                order — PAPER execution stays disabled unless a rule already has
                auto-paper on.
              </p>
              {runErr && <p className="text-[var(--critical)]">{runErr}</p>}
            </div>
          }
        />
      )}
    </ConsoleShell>
  );
}
