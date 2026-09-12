"use client";

import { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, PageTitle, Section, Table, fmtTime } from "@/components/ui";
import { reasonText } from "@/lib/reasons";

// The backend's full status vocabulary (internal/opportunity) — a filter
// chip exists for every state a row can hold, not four of nine.
const STATUSES = [
  "", "DETECTED", "CALCULATING", "QUALIFIED", "REJECTED", "EXPIRED",
  "RESERVED", "SIMULATING", "COMPLETED", "FAILED",
] as const;

// statusTone: the state a row ended in carries its severity (F10) —
// REJECTED is bad (the gate found something), EXPIRED dim (nothing was
// wrong, the moment passed), FAILED bad, COMPLETED ok.
function statusTone(s: string): "ok" | "warn" | "bad" | "dim" {
  switch (s) {
    case "QUALIFIED":
    case "RESERVED":
    case "SIMULATING":
      return "ok";
    case "COMPLETED":
      return "ok";
    case "REJECTED":
    case "FAILED":
      return "bad";
    case "EXPIRED":
      return "dim";
    default:
      return "warn";
  }
}

function fmtAge(fromIso: string | undefined, nowMs: number): string {
  if (!fromIso) return "—";
  const ms = nowMs - new Date(fromIso).getTime();
  if (!Number.isFinite(ms)) return "—";
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  return `${Math.floor(m / 60)}h ago`;
}

export default function OpportunitiesPage() {
  return (
    <Suspense>
      <OpportunitiesPageInner />
    </Suspense>
  );
}

function OpportunitiesPageInner() {
  // A deep link (Risk Center rejection reasons) names the status to
  // open on; without one the default view is the qualified set.
  const deepStatus = useSearchParams().get("status") ?? "QUALIFIED";
  const [status, setStatus] = useState<string>(deepStatus);
  const [triangle, setTriangle] = useState("");
  const history = usePoll(() => api.opportunities.history(status, 100), 10000, [status]);

  const now = Date.now();
  const rows = useMemo(() => {
    const h = history.kind === "ready" || history.kind === "error" ? history.data : undefined;
    const all = h?.opportunities ?? [];
    const needle = triangle.trim().toLowerCase();
    if (!needle) return all;
    return all.filter((o) => o.triangle_id.toLowerCase().includes(needle));
  }, [history, triangle]);

  return (
    <ConsoleShell>
      <PageTitle>Opportunities</PageTitle>
      <Section title="Persisted history">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          {STATUSES.map((s) => (
            <button
              key={s || "all"}
              onClick={() => setStatus(s)}
              className={`rounded border px-2 py-0.5 text-[12px] ${
                status === s
                  ? "border-[var(--accent)] text-[var(--accent)]"
                  : "border-[var(--border)] text-[var(--text-dim)]"
              }`}
            >
              {s || "ALL"}
            </button>
          ))}
          <input
            value={triangle}
            onChange={(e) => setTriangle(e.target.value)}
            placeholder="filter by triangle…"
            aria-label="Filter by triangle"
            className="ml-auto w-56 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-0.5 text-[12px] outline-none"
          />
        </div>
        <Await state={history} what="opportunity history">
          {() => (
            <Table
              head={["Detected", "Age", "Triangle", "Exchange", "Status", "Reason", "Input", "Net profit", "Net bps", ""]}
              empty="persisted opportunities for this filter"
              rows={rows.map((o) => [
                fmtTime(o.detected_at),
                fmtAge(o.detected_at, now),
                <Link key="t" href={`/triangles/${encodeURIComponent(o.triangle_id)}`} className="text-[var(--accent)] underline">
                  {o.triangle_id}
                </Link>,
                o.triangle_id.split("|")[0] || "—",
                <Badge key="s" tone={statusTone(o.status)}>{o.status}</Badge>,
                <span key="r" title={o.reason_code ?? ""}>{reasonText(o.reason_code)}</span>,
                `${o.starting_amount} ${o.starting_asset}`,
                o.net_profit !== undefined && o.net_profit !== null ? `${o.net_profit} ${o.starting_asset}` : "—",
                o.net_return_bps ?? "—",
                <Link key="v" href={`/opportunities/${encodeURIComponent(o.id)}`} className="text-[var(--accent)] underline">
                  detail
                </Link>,
              ])}
            />
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
