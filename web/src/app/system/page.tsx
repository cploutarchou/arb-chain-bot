"use client";

import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, PageTitle, Section, Stat } from "@/components/ui";

export default function SystemHealthPage() {
  const status = usePoll(() => api.system.status(), 5000);

  return (
    <ConsoleShell active="System Health">
      <PageTitle>System Health</PageTitle>
      <Section title="Process">
        <Await state={status} what="system status">
          {(s) => (
            <div className="grid max-w-3xl grid-cols-2 gap-3 md:grid-cols-4">
              <Stat label="Mode" value={s.mode} />
              <Stat label="Version" value={s.version} />
              <Stat label="Commit" value={s.commit || "—"} />
              <Stat label="Uptime" value={`${s.uptime_sec}s`} />
              <Stat label="Components" value={s.components.join(", ") || "none"} />
            </div>
          )}
        </Await>
      </Section>
      <p className="mt-6 max-w-2xl text-xs text-[var(--text-dim)]">
        Full health payload (per-feed state, queue depths, goroutines, GC, DB pool, clock quality)
        arrives with observability task T-035; per-exchange feed health is already available on the{" "}
        Exchanges page.
      </p>
    </ConsoleShell>
  );
}
