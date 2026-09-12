"use client";

import { useState } from "react";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, PageTitle, Section, Table, fmtTime } from "@/components/ui";

export default function AuditPage() {
  const [entity, setEntity] = useState("");
  const events = usePoll(() => api.audit(entity, 100), 10000, [entity]);

  return (
    <ConsoleShell>
      <PageTitle>Audit Log</PageTitle>
      <div className="mb-3">
        <input
          value={entity}
          onChange={(e) => setEntity(e.target.value)}
          placeholder="filter by entity (e.g. strategy_config, paper_engine)"
          className="w-80 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
        />
      </div>
      <Section title="Insert-only audit trail (requires OPERATOR+)">
        <Await state={events} what="audit events">
          {(a) => (
            <Table
              head={["Time", "Source", "Actor", "Action", "Entity", "Entity ID"]}
              empty="audit events recorded yet for this filter"
              label="Audit events"
              rowKeys={(a.events ?? []).map((e) => e.id)}
              rows={(a.events ?? []).map((e) => [
                fmtTime(e.ts),
                <Badge key="src" tone="dim">{e.source}</Badge>,
                e.actor ?? "system",
                e.action,
                e.entity,
                e.entity_id ?? "—",
              ])}
            />
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
