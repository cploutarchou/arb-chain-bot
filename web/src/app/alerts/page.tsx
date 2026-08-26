"use client";

import { useState } from "react";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, PageTitle, Section, Table, fmtTime, severityTone } from "@/components/ui";

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
      <div className="mb-3 flex gap-2">
        {STATES.map((s) => (
          <button
            key={s || "all"}
            onClick={() => setFilter(s)}
            className={`rounded border px-2 py-0.5 text-[12px] ${
              filter === s ? "border-[var(--accent)] text-[var(--accent)]" : "border-[var(--border)] text-[var(--text-dim)]"
            }`}
          >
            {s || "all"}
          </button>
        ))}
      </div>
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
                  <Badge key="st" tone={al.state === "active" ? "warn" : al.state === "acked" ? "dim" : "ok"}>
                    {al.state}
                  </Badge>,
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
