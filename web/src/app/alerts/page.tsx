"use client";

import { useState } from "react";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, PageTitle, Section, Table, fmtTime, severityTone, ChipGroup} from "@/components/ui";

const STATES = ["", "active", "acked", "resolved"] as const;

export default function AlertsPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const [filter, setFilter] = useState<string>("");
  const [refresh, setRefresh] = useState(0);
  const alerts = usePoll(() => api.alerts.list(filter, 100), 5000, [filter, refresh]);
  const mayAck = can(role, "alerts:ack");

  const act = async (fn: () => Promise<unknown>) => {
    try {
      await fn();
    } finally {
      setRefresh((n) => n + 1);
    }
  };

  return (
    <ConsoleShell active="Alerts">
      <PageTitle>Alert Center</PageTitle>
      <ChipGroup label="Filter alerts by state" options={STATES} value={filter} onChange={setFilter} format={(s) => s} />
      <Section title="Alerts (state shared with Telegram — single backend)">
        <Await state={alerts} what="alerts">
          {(a) => (
            <>
              <p className="mb-2 text-[12px] text-[var(--text-dim)]">{a.active} unresolved</p>
              <Table
                head={["Last", "Severity", "State", "Title", "Detail", "Count", ""]}
                empty="alerts for this filter"
                rows={(a.alerts ?? []).map((al) => [
                  fmtTime(al.last_at),
                  <Badge key="sev" tone={severityTone(al.severity)}>{al.severity}</Badge>,
                  <span key="st" className="flex flex-col gap-0.5">
                    <Badge tone={al.state === "active" ? "warn" : al.state === "acked" ? "dim" : "ok"}>
                      {al.state}
                    </Badge>
                    {al.state === "resolved" && (
                      <span className="text-[11px] text-[var(--text-dim)]">
                        {al.resolved_by ? `by ${al.resolved_by}` : "auto-resolved"}
                      </span>
                    )}
                  </span>,
                  al.title,
                  <span key="b" className="max-w-md truncate text-[var(--text-dim)]" title={al.body}>
                    {al.body}
                  </span>,
                  al.count > 1 ? `×${al.count}` : "1",
                  mayAck ? (
                    <span key="act" className="flex gap-1">
                      {al.state === "active" && <Button onClick={() => act(() => api.alerts.ack(al.id))}>Ack</Button>}
                      {al.state !== "resolved" && (
                        <Button onClick={() => act(() => api.alerts.resolve(al.id))}>Resolve</Button>
                      )}
                    </span>
                  ) : (
                    ""
                  ),
                ])}
              />
            </>
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
