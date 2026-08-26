"use client";

import { useState } from "react";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, PageTitle, Section, Table, fmtTime } from "@/components/ui";

const STATUSES = ["", "QUALIFIED", "REJECTED", "EXPIRED"] as const;

export default function OpportunitiesPage() {
  const [status, setStatus] = useState<string>("QUALIFIED");
  const history = usePoll(() => api.opportunities.history(status, 100), 10000, [status]);

  return (
    <ConsoleShell active="Opportunities">
      <PageTitle>Opportunities</PageTitle>
      <Section title="Persisted history">
        <div className="mb-3 flex gap-2">
          {STATUSES.map((s) => (
            <button
              key={s || "all"}
              onClick={() => setStatus(s)}
              className={`rounded border px-2 py-0.5 text-[12px] ${
                status === s
                  ? "border-[var(--accent)] text-[var(--accent)]"
                  : "border-[var(--border)] text-[var(--text-dim)]"
              }`}
            >
              {s || "ALL"}
            </button>
          ))}
        </div>
        <Await state={history} what="opportunity history">
          {(h) => (
            <Table
              head={["Detected", "Triangle", "Status", "Reason", "Input", "Net profit", "Net bps", "Quality", "Cfg"]}
              empty="persisted opportunities for this filter"
              rows={(h.opportunities ?? []).map((o) => [
                fmtTime(o.detected_at),
                o.triangle_id,
                <Badge key="s" tone={o.status === "QUALIFIED" ? "ok" : o.status === "REJECTED" ? "dim" : "warn"}>
                  {o.status}
                </Badge>,
                o.reason_code ?? "—",
                `${o.starting_amount} ${o.starting_asset}`,
                o.net_profit ?? "—",
                o.net_return_bps ?? "—",
                o.data_quality ?? "—",
                o.config_version ?? "—",
              ])}
            />
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
