"use client";

import { useState } from "react";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, PageTitle, Section, Table, fmtTime } from "@/components/ui";

const WINDOWS = [24, 72, 168, 720] as const;

export default function RiskPage() {
  const risk = usePoll(() => api.risk(), 5000);
  const [hours, setHours] = useState<number>(24);
  const events = usePoll(() => api.riskEvents.list(hours, 200), 10000, [hours]);

  return (
    <ConsoleShell active="Risk Center">
      <PageTitle>Risk Center</PageTitle>
      <Await state={risk} what="risk state">
        {(r) => (
          <>
            <Section title={`Effective limits (config v${r.config_version ?? "?"}) — deterministic engine; nothing overrides it`}>
              <Table
                head={["Limit", "Value"]}
                empty="limits"
                rows={Object.entries(r.limits ?? {}).map(([k, v]) => [k, String(v)])}
              />
            </Section>
            <Section title="Circuit breakers">
              <Table
                head={["Name", "Scope", "State", "Reason"]}
                empty="registered breakers"
                rows={(r.breakers ?? []).map((b) => [
                  b.Name,
                  b.Scope || "global",
                  <Badge key="s" tone={b.State === "OPEN" ? "bad" : b.State === "HALF_OPEN" ? "warn" : "ok"}>
                    {b.State}
                  </Badge>,
                  b.Reason || "—",
                ])}
              />
            </Section>
            <Section title="Rejection reasons (session)">
              <Table
                head={["Reason code", "Count"]}
                empty="rejections recorded"
                rows={Object.entries(r.reject_reason_counts ?? {})
                  .sort((a, b) => b[1] - a[1])
                  .map(([code, n]) => [code, n])}
              />
            </Section>
          </>
        )}
      </Await>

      <Section title="Risk event timeline (persisted — survives a restart, unlike the session counter above)">
        <div className="mb-3 flex gap-2">
          {WINDOWS.map((w) => (
            <button
              key={w}
              onClick={() => setHours(w)}
              className={`rounded border px-2 py-0.5 text-[12px] ${
                hours === w ? "border-[var(--accent)] text-[var(--accent)]" : "border-[var(--border)] text-[var(--text-dim)]"
              }`}
            >
              {w < 168 ? `${w}h` : `${Math.round(w / 24)}d`}
            </button>
          ))}
        </div>
        <Await state={events} what="risk events">
          {(res) => (
            <>
              <Table
                head={["Time", "Kind", "Subject", "Limit", "Observed", "Threshold", "Action", "Breaker state", "Correlation"]}
                empty="risk events in this window"
                sticky
                maxHeight={480}
                rows={(res.events ?? []).map((ev) => [
                  fmtTime(ev.ts),
                  <Badge key="k" tone={ev.kind === "breaker" ? "warn" : "dim"}>
                    {ev.kind}
                  </Badge>,
                  ev.subject ?? "—",
                  ev.limit_name ?? "—",
                  ev.observed ?? "—",
                  ev.threshold ?? "—",
                  ev.action ?? "—",
                  ev.breaker_state ? (
                    <Badge
                      key="b"
                      tone={ev.breaker_state === "OPEN" ? "bad" : ev.breaker_state === "HALF_OPEN" ? "warn" : "ok"}
                    >
                      {ev.breaker_state}
                    </Badge>
                  ) : (
                    "—"
                  ),
                  ev.correlation_id ?? "—",
                ])}
              />
              <p className="mt-2 text-[11px] text-[var(--text-dim)]">n = {res.n} events</p>
            </>
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
