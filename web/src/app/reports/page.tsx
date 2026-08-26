"use client";

import { useState } from "react";
import { api, ApiError, type Report } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, PageTitle, Section, Table, fmtTime } from "@/components/ui";

export default function ReportsPage() {
  const [refresh, setRefresh] = useState(0);
  const reports = usePoll(() => api.reports.list("", 20), 15000, [refresh]);
  const [selected, setSelected] = useState<Report | null>(null);
  const [msg, setMsg] = useState("");

  const generate = async (kind: "daily" | "weekly") => {
    setMsg("");
    try {
      const rep = await api.reports.generate(kind);
      setSelected(rep);
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setMsg(err instanceof ApiError ? err.message : "Generation failed.");
    }
  };

  return (
    <ConsoleShell active="Reports">
      <PageTitle>Reports</PageTitle>
      <div className="mb-3 flex gap-2">
        <Button onClick={() => generate("daily")}>Generate daily now</Button>
        <Button onClick={() => generate("weekly")}>Generate weekly now</Button>
        {msg && <span className="self-center text-[12px] text-[var(--critical)]">{msg}</span>}
      </div>
      <Section title="Persisted reports">
        <Await state={reports} what="reports">
          {(r) => (
            <Table
              head={["Generated", "Kind", "Period", "Executive summary", ""]}
              empty="persisted reports (requires ARB_DATABASE_URL)"
              rows={(r.reports ?? []).map((rep) => [
                fmtTime(rep.generated_at),
                <Badge key="k" tone="dim">{rep.kind}</Badge>,
                `${fmtTime(rep.period_start)} → ${fmtTime(rep.period_end)}`,
                <span key="s" className="max-w-lg truncate" title={rep.executive_summary}>
                  {rep.executive_summary}
                </span>,
                <Button key="v" onClick={() => setSelected(rep)}>view</Button>,
              ])}
            />
          )}
        </Await>
      </Section>
      {selected && (
        <Section title={`Report ${selected.id} (detailed)`}>
          <pre className="max-h-[32rem] overflow-auto rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3 text-[12px]">
            {JSON.stringify(selected, null, 2)}
          </pre>
        </Section>
      )}
    </ConsoleShell>
  );
}
