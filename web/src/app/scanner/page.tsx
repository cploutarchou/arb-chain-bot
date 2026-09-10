"use client";

// Scanner (BL-30): live counters + WS stream + persisted recent history,
// now with client-side filter/sort over already-fetched rows, a
// pause-display toggle (freezes the live stream without unsubscribing —
// resuming shows what arrived while paused), pin-triangle (localStorage,
// read only in an effect so SSR/CSR hydration never mismatches), and a
// CSV export of the currently visible rows.

import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api/client";
import { cmpDecimalStr, signTone, signedText } from "@/lib/decimal";
import { usePoll } from "@/lib/usePoll";
import { connectHub, type HubMessage } from "@/lib/ws";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, PageTitle, Section, Stat, Table, fmtTime } from "@/components/ui";

interface ScannerEvent {
  opportunity_id?: string;
  triangle_id?: string;
  status?: string;
  net_bps?: string;
  net_profit?: string;
  input?: string;
  detected_at?: string;
}

const PIN_KEY = "arb-console.pinned-triangles";

function loadPins(): Set<string> {
  try {
    const raw = window.localStorage.getItem(PIN_KEY);
    return raw ? new Set(JSON.parse(raw) as string[]) : new Set();
  } catch {
    return new Set();
  }
}

function savePins(pins: Set<string>) {
  try {
    window.localStorage.setItem(PIN_KEY, JSON.stringify([...pins]));
  } catch {
    // localStorage unavailable (private mode, quota) — pins just don't persist.
  }
}

function toCSV(rows: (string | number | undefined)[][]): string {
  const esc = (v: string | number | undefined) => {
    const s = String(v ?? "");
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  return rows.map((r) => r.map(esc).join(",")).join("\n");
}

function downloadCSV(filename: string, rows: (string | number | undefined)[][]) {
  const blob = new Blob([toCSV(rows)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

type SortKey = "detected_at" | "net_bps" | "triangle_id";

export default function ScannerPage() {
  const status = usePoll(() => api.scanner.status(), 3000);
  const recent = usePoll(() => api.opportunities.recent(20), 5000);
  const [live, setLive] = useState<ScannerEvent[]>([]);
  const [wsStatus, setWsStatus] = useState<"connecting" | "open" | "closed">("connecting");

  // Pause-display: the WS subscription stays open (no resync churn) and
  // the effect that opens it never re-runs on pause/resume — pausedRef
  // is read inside the message handler instead of putting `paused` in
  // the effect's deps, which would otherwise tear down and resubscribe
  // the socket (forcing a fresh snapshot) on every toggle. The buffer is
  // state, not a ref, so the "N buffered" count below is never stale.
  const [paused, setPaused] = useState(false);
  const pausedRef = useRef(false);
  const [buffer, setBuffer] = useState<ScannerEvent[]>([]);

  const [pinned, setPinned] = useState<Set<string>>(new Set());
  useEffect(() => setPinned(loadPins()), []); // read localStorage post-hydration only

  const [triangleFilter, setTriangleFilter] = useState("");
  const [minBps, setMinBps] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("detected_at");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc");
  const [pinnedOnly, setPinnedOnly] = useState(false);

  useEffect(() => {
    return connectHub(["scanner"], {
      onStatus: setWsStatus,
      onMessage: (msg: HubMessage) => {
        const ev = msg.data as ScannerEvent;
        if (msg.snapshot || !ev?.opportunity_id) return; // snapshots carry counters, not events
        if (pausedRef.current) {
          setBuffer((prev) => [ev, ...prev].slice(0, 30));
          return;
        }
        setLive((prev) => [ev, ...prev].slice(0, 30));
      },
    });
  }, []);

  const togglePause = () => {
    const next = !paused;
    pausedRef.current = next;
    setPaused(next);
    if (!next) {
      // Resuming: flush whatever queued while paused. Read the buffer
      // from inside its own updater (never from the enclosing closure)
      // so this never races a state value it is also clearing.
      setBuffer((buf) => {
        setLive((prevLive) => [...buf, ...prevLive].slice(0, 30));
        return [];
      });
    }
  };

  const togglePin = (triangleID: string) => {
    setPinned((prev) => {
      const next = new Set(prev);
      if (next.has(triangleID)) next.delete(triangleID);
      else next.add(triangleID);
      savePins(next);
      return next;
    });
  };

  const filterSort = <T extends { triangle_id: string; net_bps?: string; detected_at?: string; at?: string }>(
    rows: T[],
  ): T[] => {
    let out = rows;
    if (triangleFilter.trim()) {
      const needle = triangleFilter.trim().toLowerCase();
      out = out.filter((r) => r.triangle_id.toLowerCase().includes(needle));
    }
    if (minBps.trim()) {
      out = out.filter((r) => cmpDecimalStr(r.net_bps ?? "0", minBps.trim()) >= 0);
    }
    if (pinnedOnly) out = out.filter((r) => pinned.has(r.triangle_id));
    const dir = sortDir === "asc" ? 1 : -1;
    return [...out].sort((a, b) => {
      if (sortKey === "net_bps") return dir * cmpDecimalStr(a.net_bps ?? "0", b.net_bps ?? "0");
      if (sortKey === "triangle_id") return dir * a.triangle_id.localeCompare(b.triangle_id);
      const ta = new Date(a.detected_at ?? a.at ?? 0).getTime();
      const tb = new Date(b.detected_at ?? b.at ?? 0).getTime();
      return dir * (ta - tb);
    });
  };

  const visibleLive = useMemo(
    () => filterSort(live.filter((e): e is ScannerEvent & { triangle_id: string } => !!e.triangle_id)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [live, triangleFilter, minBps, sortKey, sortDir, pinnedOnly, pinned],
  );
  const recentRows = recent.kind === "ready" ? (recent.data.opportunities ?? []) : [];
  const visibleRecent = useMemo(
    () => filterSort(recentRows),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [recentRows, triangleFilter, minBps, sortKey, sortDir, pinnedOnly, pinned],
  );

  const PinButton = ({ id }: { id: string }) => (
    <button
      onClick={() => togglePin(id)}
      aria-pressed={pinned.has(id)}
      aria-label={pinned.has(id) ? `Unpin triangle ${id}` : `Pin triangle ${id}`}
      className={`rounded border px-1.5 py-0.5 text-[11px] ${
        pinned.has(id) ? "border-[var(--accent)] text-[var(--accent)]" : "border-[var(--border)] text-[var(--text-dim)]"
      }`}
      title={pinned.has(id) ? "Unpin" : "Pin"}
    >
      {pinned.has(id) ? "★" : "☆"}
    </button>
  );

  return (
    <ConsoleShell active="Scanner">
      <PageTitle>Scanner</PageTitle>
      <Section title="Live counters">
        <Await state={status} what="scanner status">
          {(s) => (
            <div className="grid max-w-4xl grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-6">
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

      <Section title="Filters">
        <div className="flex flex-wrap items-end gap-3 text-[13px]">
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-[var(--text-dim)]">Triangle contains</span>
            <input
              value={triangleFilter}
              onChange={(e) => setTriangleFilter(e.target.value)}
              className="w-40 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-[var(--text-dim)]">Min net bps</span>
            <input
              value={minBps}
              onChange={(e) => setMinBps(e.target.value)}
              type="number"
              className="w-28 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-[var(--text-dim)]">Sort by</span>
            <select
              value={sortKey}
              onChange={(e) => setSortKey(e.target.value as SortKey)}
              className="rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
            >
              <option value="detected_at">Time</option>
              <option value="net_bps">Net bps</option>
              <option value="triangle_id">Triangle</option>
            </select>
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-[var(--text-dim)]">Direction</span>
            <select
              value={sortDir}
              onChange={(e) => setSortDir(e.target.value as "asc" | "desc")}
              className="rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
            >
              <option value="desc">desc</option>
              <option value="asc">asc</option>
            </select>
          </label>
          <label className="flex items-center gap-2 pb-1">
            <input type="checkbox" checked={pinnedOnly} onChange={(e) => setPinnedOnly(e.target.checked)} />
            <span>Pinned only ({pinned.size})</span>
          </label>
        </div>
      </Section>

      <Section
        title={`Live qualified stream (WS ${wsStatus === "open" ? "connected" : wsStatus})`}
      >
        <div className="mb-2 flex items-center gap-2">
          <Button onClick={togglePause}>{paused ? "Resume display" : "Pause display"}</Button>
          {paused && (
            <span className="text-[12px] text-[var(--warn)]">
              paused — {buffer.length} event(s) buffered, resume to show them
            </span>
          )}
          <Button
            onClick={() =>
              downloadCSV(
                "scanner-live.csv",
                [
                  ["detected_at", "triangle_id", "net_bps", "net_profit", "input"],
                  ...visibleLive.map((e) => [e.detected_at, e.triangle_id, e.net_bps, e.net_profit, e.input]),
                ],
              )
            }
            disabled={visibleLive.length === 0}
          >
            Export CSV
          </Button>
        </div>
        <div aria-live="polite" aria-atomic="false">
          <Table
            head={["", "Detected", "Triangle", "Net bps", "Net profit", "Input"]}
            empty="live events yet (stream fills as opportunities qualify)"
            rows={visibleLive.map((ev) => [
              <PinButton key="pin" id={ev.triangle_id} />,
              ev.detected_at ? fmtTime(ev.detected_at) : "—",
              <Link key="t" href={`/triangles/${encodeURIComponent(ev.triangle_id)}`} className="text-[var(--accent)] underline">
                {ev.triangle_id}
              </Link>,
              <Badge key="b" tone={signTone(ev.net_bps)}>{signedText(ev.net_bps)}</Badge>,
              ev.net_profit ?? "—",
              ev.input ?? "—",
            ])}
          />
        </div>
      </Section>

      <Section title="Recent qualified opportunities (in-memory ring)">
        <div className="mb-2">
          <Button
            onClick={() =>
              downloadCSV(
                "scanner-recent.csv",
                [
                  ["id", "at", "triangle_id", "net_bps", "profit", "input"],
                  ...visibleRecent.map((o) => [o.id, o.at, o.triangle_id, o.net_bps, o.profit, o.input]),
                ],
              )
            }
            disabled={visibleRecent.length === 0}
          >
            Export CSV
          </Button>
        </div>
        <Await state={recent} what="recent opportunities">
          {() => (
            <Table
              head={["", "Detected", "Triangle", "Net bps", "Net profit", "Input", "ID"]}
              empty="qualified opportunities in the recent window"
              rows={visibleRecent.map((o) => [
                <PinButton key="pin" id={o.triangle_id} />,
                fmtTime(o.at),
                <Link key="t" href={`/triangles/${encodeURIComponent(o.triangle_id)}`} className="text-[var(--accent)] underline">
                  {o.triangle_id}
                </Link>,
                <Badge key="b" tone={signTone(o.net_bps)}>{signedText(o.net_bps)}</Badge>,
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
