"use client";

// System Health (BL-18): the full payload — process, DB pool, queue
// depths, message rates, per-exchange feed latency/reconnects/sequence
// errors, and restart state — on usePoll. Every section is independently
// optional (mirrors the backend: process stats are always present, the
// rest only when their component exists in this profile) and renders
// "not running in this profile" rather than a faked zero when absent.

import { api } from "@/lib/api/client";
import { bookAgeText, fmtBytes, fmtDurationSec } from "@/lib/format";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, PageTitle, Section, Stat, Table } from "@/components/ui";

export default function SystemHealthPage() {
  const status = usePoll(() => api.system.status(), 10000);
  const health = usePoll(() => api.system.healthFull(), 5000);

  return (
    <ConsoleShell>
      <PageTitle>System Health</PageTitle>

      <Section title="Process">
        <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
          {status.kind === "ready" && (
            <>
              <Stat label="Mode" value={status.data.mode} />
              <Stat label="Version" value={status.data.version} />
              <Stat label="Commit" value={status.data.commit || "—"} />
              <Stat label="Components" value={status.data.components.join(", ") || "none"} />
            </>
          )}
          {health.kind === "ready" && (
            <>
              <Stat label="Uptime" value={fmtDurationSec(health.data.process.uptime_sec)} />
              <Stat label="Goroutines" value={health.data.process.goroutines} />
              <Stat label="Heap alloc" value={fmtBytes(health.data.process.heap_alloc_bytes)} />
              <Stat label="Heap sys" value={fmtBytes(health.data.process.heap_sys_bytes)} />
              <Stat label="Process sys" value={fmtBytes(health.data.process.sys_bytes)} />
              <Stat label="GC runs" value={health.data.process.num_gc} />
              <Stat label="GC pause (total)" value={`${(health.data.process.gc_pause_total_ns / 1e6).toFixed(1)} ms`} />
            </>
          )}
        </div>
      </Section>

      <Await state={health} what="system health">
        {(h) => (
          <>
            {h.ready !== undefined && (
              <Section title="Engine / scanner">
                <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
                  <Stat label="Ready" value={h.ready ? "yes" : "no"} tone={h.ready ? "ok" : "warn"} />
                  <Stat label="Triangles" value={h.triangles ?? "—"} />
                  {h.scanner &&
                    Object.entries(h.scanner).map(([k, v]) => <Stat key={k} label={k} value={v} />)}
                </div>
              </Section>
            )}

            {h.feed && (
              <Section title="Market-data feed">
                <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
                  <Stat label="Frames" value={h.feed.frames} />
                  <Stat label="Msgs/sec" value={h.feed.msgs_per_sec.toFixed(2)} />
                  <Stat
                    label="Reconnects"
                    value={h.feed.reconnects}
                    tone={h.feed.reconnects > 0 ? "warn" : undefined}
                  />
                  <Stat label="API errors" value={h.feed.api_errors} tone={h.feed.api_errors > 0 ? "warn" : undefined} />
                  <Stat label="Resyncs" value={h.feed.resyncs} tone={h.feed.resyncs > 0 ? "warn" : undefined} />
                  <Stat
                    label="Sequence gaps"
                    value={h.feed.seq_gaps}
                    tone={h.feed.seq_gaps > 0 ? "bad" : undefined}
                  />
                  {h.feed.latency_ms && (
                    <>
                      <Stat label="Latency p50" value={`${h.feed.latency_ms.p50_ms.toFixed(1)} ms`} />
                      <Stat label="Latency p95" value={`${h.feed.latency_ms.p95_ms.toFixed(1)} ms`} />
                      <Stat label="Latency p99" value={`${h.feed.latency_ms.p99_ms.toFixed(1)} ms`} />
                      <Stat label="Latency samples" value={h.feed.latency_ms.n} />
                    </>
                  )}
                </div>
              </Section>
            )}

            {h.books && h.books.length > 0 && (
              <Section title="Order books">
                <Table
                  head={["Market", "State", "Age (ms)"]}
                  empty="order books"
                  label="Order book health"
                rowKeys={h.books.map((b) => b.market)}
                rows={h.books.map((b) => [
                    b.market,
                    <Badge
                      key="s"
                      tone={b.state === "HEALTHY" ? "ok" : b.state === "SYNCING" || b.state === "STALE" ? "warn" : "bad"}
                    >
                      {b.state}
                    </Badge>,
                    bookAgeText(b.age_ms),
                  ])}
                />
              </Section>
            )}

            {h.paper && (
              <Section title="Paper engine">
                <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
                  <Stat label="Running" value={h.paper.running ? "yes" : "no"} tone={h.paper.running ? "ok" : "warn"} />
                  <Stat label="Active simulations" value={h.paper.active_simulations} />
                  <Stat label="Received" value={h.paper.received} />
                  <Stat label="Completed" value={h.paper.completed} />
                  <Stat label="Failed" value={h.paper.failed} tone={h.paper.failed > 0 ? "warn" : undefined} />
                  <Stat label="Skipped" value={h.paper.skipped} />
                  <Stat label="Dropped (queue full)" value={h.paper.dropped ?? 0} tone={(h.paper.dropped ?? 0) > 0 ? "warn" : undefined} />
                </div>
              </Section>
            )}

            {h.queues && (
              <Section title="Queue depths">
                <Table
                  head={["Queue", "Depth", "Capacity", "Dropped", "Written", "Write failures", "Unlinked cycles"]}
                  empty="queues"
                  label="Write queues"
                  // Keyed by queue name, not position. The list is
                  // filtered on presence, so a queue the health payload
                  // stops reporting shifts every row below it — and an
                  // index key would then apply one queue's figures to
                  // another queue's DOM row.
                  rowKeys={[
                    { name: "outbox", q: h.queues.outbox },
                    { name: "paper", q: h.queues.paper },
                    { name: "recorder", q: h.queues.recorder },
                  ]
                    .filter((row) => row.q !== undefined)
                    .map(({ name }) => name)}
                  rows={[
                    { name: "outbox", q: h.queues.outbox },
                    { name: "paper", q: h.queues.paper },
                    { name: "recorder", q: h.queues.recorder },
                  ]
                    .filter((row) => row.q !== undefined)
                    .map(({ name, q }) => [
                      q!.failing ? `${name} (writes failing)` : name,
                      q!.depth,
                      q!.capacity,
                      q!.dropped ?? "—",
                      q!.written ?? "—",
                      q!.write_failures ?? "—",
                      q!.unlinked_cycles ?? "—",
                    ])}
                />
              </Section>
            )}

            {h.database && (
              <Section title="Database pool">
                <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
                  <Stat label="Total conns" value={h.database.total_conns} />
                  <Stat label="Acquired" value={h.database.acquired_conns} />
                  <Stat label="Idle" value={h.database.idle_conns} />
                  <Stat label="Constructing" value={h.database.constructing_conns} />
                  <Stat label="Max conns" value={h.database.max_conns} />
                  <Stat label="Acquire count" value={h.database.acquire_count} />
                  <Stat label="Empty acquire count" value={h.database.empty_acquire_count} tone={h.database.empty_acquire_count > 0 ? "warn" : undefined} />
                  <Stat label="Canceled acquires" value={h.database.canceled_acquire_count} tone={h.database.canceled_acquire_count > 0 ? "warn" : undefined} />
                </div>
              </Section>
            )}
            {!h.database && (
              <Section title="Database pool">
                <p className="text-sm text-[var(--text-dim)]">
                  Persistence isn&apos;t configured for this deployment — no database pool to report.
                </p>
              </Section>
            )}

            {h.restart && (
              <Section title="Restart / supervisor state">
                <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
                  <Stat
                    label="State"
                    value={h.restart.state}
                    tone={h.restart.state === "failed" ? "bad" : h.restart.state === "ready" ? "ok" : "warn"}
                  />
                  <Stat label="Settings version" value={h.restart.settings_version} />
                  <Stat label="Pending version" value={h.restart.pending_version ?? "—"} />
                  <Stat label="Restarts" value={h.restart.restarts} />
                  <Stat label="Requested by" value={h.restart.requested_by ?? "—"} />
                  <Stat label="Requested at" value={h.restart.requested_at ?? "—"} />
                  <Stat label="Ready at" value={h.restart.ready_at ?? "—"} />
                </div>
                {(h.restart.pending_reasons?.length ?? 0) > 0 && (
                  <p className="mt-2 text-[12px] text-[var(--warn)]">
                    {h.restart.pending_reasons!.join("; ")}
                  </p>
                )}
                {h.restart.error && <p className="mt-2 text-[12px] text-[var(--critical)]">{h.restart.error}</p>}
              </Section>
            )}
          </>
        )}
      </Await>
    </ConsoleShell>
  );
}
