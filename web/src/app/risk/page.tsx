"use client";

import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, PageTitle, Section, Table } from "@/components/ui";

export default function RiskPage() {
  const risk = usePoll(() => api.risk(), 5000);

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
    </ConsoleShell>
  );
}
