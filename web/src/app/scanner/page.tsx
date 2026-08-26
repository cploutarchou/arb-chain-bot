"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { connectHub, type HubMessage } from "@/lib/ws";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, PageTitle, Section, Stat, Table, fmtTime } from "@/components/ui";

interface ScannerEvent {
  opportunity_id?: string;
  triangle_id?: string;
  status?: string;
  net_bps?: string;
  net_profit?: string;
  input?: string;
  detected_at?: string;
}

export default function ScannerPage() {
  const status = usePoll(() => api.scanner.status(), 3000);
  const recent = usePoll(() => api.opportunities.recent(20), 5000);
  const [live, setLive] = useState<ScannerEvent[]>([]);
  const [wsStatus, setWsStatus] = useState<"connecting" | "open" | "closed">("connecting");

  useEffect(() => {
    return connectHub(["scanner"], {
      onStatus: setWsStatus,
      onMessage: (msg: HubMessage) => {
        const ev = msg.data as ScannerEvent;
        if (msg.snapshot || !ev?.opportunity_id) return; // snapshots carry counters, not events
        setLive((prev) => [ev, ...prev].slice(0, 30));
      },
    });
  }, []);

  return (
    <ConsoleShell active="Scanner">
      <PageTitle>Scanner</PageTitle>
      <Section title="Live counters">
        <Await state={status} what="scanner status">
          {(s) => (
            <div className="grid max-w-4xl grid-cols-2 gap-3 md:grid-cols-6">
              <Stat label="Ready" value={s.ready ? "yes" : "no"} tone={s.ready ? "ok" : "warn"} />
              <Stat label="Triangles" value={s.triangles} />
              <Stat label="Evaluations" value={s.evaluations} />
              <Stat label="Qualified" value={s.qualified} tone="ok" />
              <Stat label="Rejected" value={s.rejected} />
              <Stat label="Skipped (unhealthy)" value={s.skipped_unhealthy} tone={s.skipped_unhealthy > 0 ? "warn" : undefined} />
            </div>
          )}
        </Await>
      </Section>
      <Section
        title={`Live qualified stream (WS ${wsStatus === "open" ? "connected" : wsStatus})`}
      >
        <Table
          head={["Detected", "Triangle", "Net bps", "Profit", "Input"]}
          empty="live events yet (stream fills as opportunities qualify)"
          rows={live.map((ev) => [
            ev.detected_at ? fmtTime(ev.detected_at) : "—",
            ev.triangle_id ?? "—",
            <Badge key="b" tone="ok">{ev.net_bps ?? "—"}</Badge>,
            ev.net_profit ?? "—",
            ev.input ?? "—",
          ])}
        />
      </Section>
      <Section title="Recent qualified opportunities (in-memory ring)">
        <Await state={recent} what="recent opportunities">
          {(r) => (
            <Table
              head={["Detected", "Triangle", "Net bps", "Profit", "Input", "ID"]}
              empty="qualified opportunities in the recent window"
              rows={(r.opportunities ?? []).map((o) => [
                fmtTime(o.at),
                o.triangle_id,
                <Badge key="b" tone="ok">{o.net_bps}</Badge>,
                o.profit,
                o.input,
                <span key="id" className="text-[var(--text-dim)]">{o.id}</span>,
              ])}
            />
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
