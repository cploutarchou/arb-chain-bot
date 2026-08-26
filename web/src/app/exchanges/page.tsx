"use client";

import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, PageTitle, Section, Stat, Table } from "@/components/ui";

export default function ExchangesPage() {
  const health = usePoll(() => api.system.health(), 3000);

  return (
    <ConsoleShell active="Exchanges">
      <PageTitle>Exchanges &amp; Markets</PageTitle>
      <Await state={health} what="exchange health">
        {(h) => (
          <>
            <Section title="Binance feed (public market data only — no keys, no withdrawal permissions ever)">
              {h.feed ? (
                <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-5">
                  <Stat label="Frames" value={h.feed.frames} />
                  <Stat label="Reconnects" value={h.feed.reconnects} tone={h.feed.reconnects > 5 ? "warn" : undefined} />
                  <Stat label="REST errors" value={h.feed.api_errors} tone={h.feed.api_errors > 0 ? "warn" : undefined} />
                  <Stat label="Resyncs" value={h.feed.resyncs} />
                  <Stat label="Sequence gaps" value={h.feed.seq_gaps} tone={h.feed.seq_gaps > 0 ? "warn" : undefined} />
                </div>
              ) : (
                <p className="text-sm text-[var(--text-dim)]">Feed not started yet.</p>
              )}
            </Section>
            <Section title="Order books">
              <Table
                head={["Market", "State", "Age (ms)"]}
                empty="books (engine still bootstrapping)"
                rows={(h.books ?? []).map((b) => [
                  b.market,
                  <Badge
                    key="s"
                    tone={b.state === "HEALTHY" ? "ok" : b.state === "STALE" || b.state === "SYNCING" ? "warn" : "bad"}
                  >
                    {b.state}
                  </Badge>,
                  b.age_ms,
                ])}
              />
            </Section>
          </>
        )}
      </Await>
    </ConsoleShell>
  );
}
