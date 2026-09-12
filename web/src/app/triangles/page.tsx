"use client";

import { useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, PageTitle, Section, Table, ChipGroup} from "@/components/ui";

const WINDOWS = [24, 72, 168] as const;

export default function TrianglesPage() {
  const [hours, setHours] = useState<number>(24);
  const quality = usePoll(() => api.triangles.quality(hours), 15000, [hours]);
  const status = usePoll(() => api.scanner.status(), 10000);

  return (
    <ConsoleShell>
      <PageTitle>Triangles</PageTitle>
      <Section title="Active topology">
        <Await state={status} what="topology">
          {(s) => (
            <p className="text-sm text-[var(--text-dim)]">
              {s.triangles} triangles across {(s.markets ?? []).length} markets
              {s.markets && s.markets.length > 0 ? `: ${s.markets.join(", ")}` : ""}
            </p>
          )}
        </Await>
      </Section>
      <Section title="Quality score (/100, SKILL §81 — never pure win rate)">
        <ChipGroup label="Quality window" options={WINDOWS} value={hours} onChange={setHours} format={(w) => `${w}h`} />
        <Await state={quality} what="quality scores">
          {(q) => (
            <>
              <Table
                head={["Triangle", "Score", "Cycles", "Components", "Notes"]}
                empty="scored triangles in this window (requires persisted history)"
                rows={(q.scores ?? []).map((s) => [
                  <Link key="id" href={`/triangles/${encodeURIComponent(s.triangle_id)}`} className="text-[var(--accent)] underline">
                    {s.triangle_id}
                  </Link>,
                  <Badge key="t" tone={s.total >= 70 ? "ok" : s.total >= 40 ? "warn" : "bad"}>
                    {s.total}
                  </Badge>,
                  s.cycles,
                  <span key="c" className="text-[12px] text-[var(--text-dim)]">
                    {Object.entries(s.components)
                      .map(([k, v]) => `${k} ${v}`)
                      .join(" · ")}
                  </span>,
                  (s.notes ?? []).join("; ") || "—",
                ])}
              />
              <p className="mt-2 text-[11px] text-[var(--text-dim)]">{q.notes.join(" · ")}</p>
            </>
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
