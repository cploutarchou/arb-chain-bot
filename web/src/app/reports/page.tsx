"use client";

// Reports (BL-32): formatted detail view (executive summary + every
// section rendered, not a raw JSON dump) plus CSV and structured JSON
// download. The CSV route is a cookie-authenticated GET with
// Content-Disposition: attachment — a plain anchor downloads it, no
// fetch/Blob needed. JSON download is a client-side Blob of the report
// already on the page.

import { useState } from "react";
import { api, ApiError, type Report } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import { OutcomeBadge } from "@/components/OutcomeBadge";
import { Await, Badge, Button, ErrorBox, Loading, PageTitle, Section, Table, fmtTime } from "@/components/ui";

function downloadJSON(report: Report) {
  const blob = new Blob([JSON.stringify(report, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `report-${report.id}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

function KV({ pairs }: { pairs: [string, React.ReactNode][] }) {
  return (
    <dl className="grid grid-cols-1 gap-x-4 gap-y-1 sm:grid-cols-2 text-[13px] md:grid-cols-4">
      {pairs.map(([k, v]) => (
        <div key={k}>
          <dt className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">{k}</dt>
          <dd className="text-[var(--text)]">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

function ReportDetailView({ report }: { report: Report }) {
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <Button onClick={() => downloadJSON(report)}>Download JSON</Button>
        <a
          href={api.reports.csvUrl(report.id)}
          className="rounded border border-[var(--border)] px-2.5 py-1 text-[12px] font-medium text-[var(--text)] transition-colors hover:bg-[var(--bg-raised)]"
        >
          Download CSV
        </a>
      </div>

      <div>
        <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
          Executive summary
        </div>
        <p className="text-[13px] leading-relaxed">{report.executive_summary}</p>
      </div>

      {report.system_health && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">System</div>
          <KV
            pairs={[
              ["Mode", report.system_health.mode],
              ["Ready", report.system_health.ready ? "yes" : "no"],
              ["Config version", `v${report.system_health.config_version}`],
              ["Active alerts", report.system_health.active_alerts],
            ]}
          />
        </div>
      )}

      {report.exchange_health && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Exchange health — {report.exchange_health.exchange}
          </div>
          <KV
            pairs={[
              ["Frames", report.exchange_health.frames],
              ["Reconnects", report.exchange_health.reconnects],
              ["API errors", report.exchange_health.api_errors],
              ["Resyncs", report.exchange_health.resyncs],
              ["Sequence gaps", report.exchange_health.sequence_gaps],
              ["Books healthy", `${report.exchange_health.books_healthy}/${report.exchange_health.books_total}`],
            ]}
          />
        </div>
      )}

      {report.scanner && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">Scanner</div>
          <KV
            pairs={[
              ["Evaluations", report.scanner.evaluations],
              ["Qualified", report.scanner.qualified],
              ["Rejected", report.scanner.rejected],
              ["Skipped", report.scanner.skipped],
              ["Dropped", report.scanner.dropped],
              ["Qualification rate", report.scanner.qualification_rate],
            ]}
          />
        </div>
      )}

      {report.opportunities && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Opportunities {report.opportunities.from_memory && <Badge tone="warn">from memory, not DB</Badge>}
          </div>
          <KV
            pairs={[
              ["Qualified (persisted)", report.opportunities.qualified_persisted],
              ["Best net bps", report.opportunities.best_net_bps ?? "—"],
              ["Avg net bps", report.opportunities.avg_net_bps ?? "—"],
            ]}
          />
        </div>
      )}

      {report.paper_cycles && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Paper cycles
          </div>
          <KV
            pairs={[
              ["Total", report.paper_cycles.total],
              ["Success", report.paper_cycles.success],
              ["Failed", report.paper_cycles.failed],
              ["Success rate", report.paper_cycles.success_rate],
            ]}
          />
        </div>
      )}

      {report.pnl && report.pnl.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">P&amp;L by asset</div>
          <Table
            head={["Asset", "Realized", "Fees", "Drawdown"]}
            empty="pnl rows"
            rows={report.pnl.map((p) => [p.asset, p.realized, p.fees, p.drawdown])}
          />
        </div>
      )}

      {report.slippage && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">Slippage</div>
          <KV
            pairs={[
              ["Avg bps", report.slippage.avg_bps ?? "—"],
              ["Worst bps", report.slippage.worst_bps ?? "—"],
              ["Samples", report.slippage.samples],
            ]}
          />
        </div>
      )}

      {report.capital_utilization && report.capital_utilization.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Capital utilization
          </div>
          <Table
            head={["Asset", "Available", "Reserved", "Utilization"]}
            empty="capital rows"
            rows={report.capital_utilization.map((c) => [c.asset, c.available, c.reserved, c.utilization])}
          />
        </div>
      )}

      {report.top_triangles && report.top_triangles.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Top triangles
          </div>
          <Table
            head={["Triangle", "Cycles", "Net P&L"]}
            empty="top triangles"
            rows={report.top_triangles.map((t) => [t.triangle_id, t.cycles, t.net_pnl])}
          />
        </div>
      )}

      {report.worst_triangles && report.worst_triangles.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Worst triangles
          </div>
          <Table
            head={["Triangle", "Cycles", "Net P&L"]}
            empty="worst triangles"
            rows={report.worst_triangles.map((t) => [t.triangle_id, t.cycles, t.net_pnl])}
          />
        </div>
      )}

      {report.failed_cycles && report.failed_cycles.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Failed cycles
          </div>
          <Table
            head={["Cycle", "Outcome"]}
            empty="failed cycles"
            rows={report.failed_cycles.map((f) => [f.id, <OutcomeBadge key="o" code={f.outcome} />])}
          />
        </div>
      )}

      {report.risk_events && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Risk events
          </div>
          <KV
            pairs={[
              ["Breakers open", report.risk_events.breakers_open],
              [
                "Reject reasons",
                Object.entries(report.risk_events.reject_reasons ?? {})
                  .map(([k, v]) => `${k}: ${v}`)
                  .join(", ") || "none",
              ],
            ]}
          />
        </div>
      )}

      {report.ai_findings && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            AI findings
          </div>
          <KV
            pairs={[
              ["Available", report.ai_findings.available ? "yes" : "no"],
              ["Proposed recommendations", report.ai_findings.proposed_recommendations],
              ["Latest summary", report.ai_findings.latest_summary ?? "—"],
            ]}
          />
        </div>
      )}

      {report.incidents && report.incidents.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Incidents
          </div>
          <Table
            head={["Severity", "Title", "Count", "Last at"]}
            empty="incidents"
            rows={report.incidents.map((i) => [
              <Badge key="s" tone={i.severity === "CRITICAL" ? "bad" : i.severity === "HIGH" ? "high" : i.severity === "WARNING" ? "warn" : "dim"}>
                {i.severity}
              </Badge>,
              i.title,
              i.count,
              fmtTime(i.last_at),
            ])}
          />
        </div>
      )}

      {report.recommended_actions.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Recommended actions
          </div>
          <ul className="list-disc space-y-1 pl-5 text-[13px]">
            {report.recommended_actions.map((a, i) => (
              <li key={i}>{a}</li>
            ))}
          </ul>
        </div>
      )}

      {report.notes && report.notes.length > 0 && (
        <p className="text-[12px] text-[var(--text-dim)]">{report.notes.join(" · ")}</p>
      )}
    </div>
  );
}

export default function ReportsPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayGenerate = can(role, "reports:generate");
  const [refresh, setRefresh] = useState(0);
  const reports = usePoll(() => api.reports.list("", 20), 15000, [refresh]);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [detail, setDetail] = useState<
    { kind: "loading" } | { kind: "error"; message: string } | { kind: "ready"; report: Report } | null
  >(null);
  const [msg, setMsg] = useState("");

  const generate = async (kind: "daily" | "weekly") => {
    setMsg("");
    try {
      const rep = await api.reports.generate(kind);
      setSelectedID(rep.id);
      setDetail({ kind: "ready", report: rep });
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setMsg(err instanceof ApiError ? err.message : "Generation failed.");
    }
  };

  const view = async (id: string) => {
    setSelectedID(id);
    setDetail({ kind: "loading" });
    try {
      const rep = await api.reports.get(id);
      setDetail({ kind: "ready", report: rep });
    } catch (err: unknown) {
      setDetail({ kind: "error", message: err instanceof ApiError ? err.message : "Failed to load report." });
    }
  };

  return (
    <ConsoleShell>
      <PageTitle>Reports</PageTitle>
      <div className="mb-3 flex items-center gap-2">
        {mayGenerate ? (
          <>
            <Button onClick={() => generate("daily")}>Generate daily now</Button>
            <Button onClick={() => generate("weekly")}>Generate weekly now</Button>
          </>
        ) : (
          <span className="text-[12px] text-[var(--text-dim)]">Generating reports requires OPERATOR or ADMIN.</span>
        )}
        {msg && <span className="self-center text-[12px] text-[var(--critical)]">{msg}</span>}
      </div>
      <Section title="Persisted reports">
        <Await state={reports} what="reports">
          {(r) => (
            <Table
              head={["Generated", "Kind", "Period", "Executive summary", ""]}
              empty={
                mayGenerate
                  ? "reports yet. Generate one above, or persistence isn't configured for this deployment — see docs/deployment.md"
                  : "reports yet. An OPERATOR or ADMIN can generate one, or persistence isn't configured for this deployment — see docs/deployment.md"
              }
              rows={(r.reports ?? []).map((rep) => [
                fmtTime(rep.generated_at),
                <Badge key="k" tone="dim">{rep.kind}</Badge>,
                `${fmtTime(rep.period_start)} → ${fmtTime(rep.period_end)}`,
                <span key="s" className="max-w-lg truncate" title={rep.executive_summary}>
                  {rep.executive_summary}
                </span>,
                <Button key="v" onClick={() => view(rep.id)}>view</Button>,
              ])}
            />
          )}
        </Await>
      </Section>
      {selectedID && detail && (
        <Section title={`Report ${selectedID}`}>
          {detail.kind === "loading" && <Loading what="report" />}
          {detail.kind === "error" && <ErrorBox message={detail.message} />}
          {detail.kind === "ready" && <ReportDetailView report={detail.report} />}
        </Section>
      )}
    </ConsoleShell>
  );
}
