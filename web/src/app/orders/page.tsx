"use client";

// Orders (BL-20): global, filterable order list — symbol, triangle,
// cycle, status, time range — with cursor pagination ("Load more") and
// cross-links order→cycle (Fills, filtered)→triangle→opportunity.
// Row virtualization (VirtualTable, BL-22) kicks in once loaded rows
// exceed ~500, which a paper session can accumulate over weeks.

import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { orderFillStatusTone } from "@/lib/tones";
import { api, ApiError, type ListFilter, type OrderListRow } from "@/lib/api/client";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Badge, Button, ErrorBox, Loading, PageTitle, Section, VirtualTable, fmtTime } from "@/components/ui";

type ListState =
  | { kind: "loading" }
  | { kind: "error"; message: string; status?: number; code?: string }
  | {
      kind: "ready";
      rows: OrderListRow[];
      nextCursor?: string;
      loadingMore: boolean;
      // A failed "Load more" keeps the rows already on screen (audit F7 —
      // unlike the initial load, a pagination failure must never blank a
      // table that already has real data) and reports what failed next
      // to the button, which doubles as the retry action.
      loadMoreError?: { message: string; status?: number; code?: string };
    };

const STATUSES = ["", "NEW", "FILLED", "PARTIAL", "REJECTED", "CANCELED"];

function OrdersPageInner() {
  const searchParams = useSearchParams();
  const [symbol, setSymbol] = useState("");
  const [triangle, setTriangle] = useState("");
  const [cycle, setCycle] = useState(searchParams.get("cycle") ?? "");
  const [status, setStatus] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  const [state, setState] = useState<ListState>({ kind: "loading" });

  const filter = (cursor?: string): ListFilter => ({
    symbol: symbol || undefined,
    triangle: triangle || undefined,
    cycle: cycle || undefined,
    status: status || undefined,
    from: from ? new Date(from).toISOString() : undefined,
    to: to ? new Date(to).toISOString() : undefined,
    limit: 100,
    cursor,
  });

  const load = () => {
    setState({ kind: "loading" });
    api.orders
      .list(filter())
      .then((page) =>
        setState({ kind: "ready", rows: page.orders ?? [], nextCursor: page.next_cursor, loadingMore: false }),
      )
      .catch((err: unknown) => {
        if (err instanceof ApiError) {
          setState({ kind: "error", message: err.message, status: err.status, code: err.apiError?.code });
        } else {
          setState({ kind: "error", message: "Backend unreachable" });
        }
      });
  };

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(load, [symbol, triangle, cycle, status, from, to]);

  const loadMore = async () => {
    if (state.kind !== "ready" || !state.nextCursor) return;
    setState({ ...state, loadingMore: true, loadMoreError: undefined });
    try {
      const page = await api.orders.list(filter(state.nextCursor));
      setState((prev) =>
        prev.kind === "ready"
          ? { kind: "ready", rows: [...prev.rows, ...(page.orders ?? [])], nextCursor: page.next_cursor, loadingMore: false }
          : prev,
      );
    } catch (err: unknown) {
      setState((prev) =>
        prev.kind === "ready"
          ? {
              ...prev,
              loadingMore: false,
              loadMoreError:
                err instanceof ApiError
                  ? { message: err.message, status: err.status, code: err.apiError?.code }
                  : { message: "Backend unreachable" },
            }
          : prev,
      );
    }
  };

  return (
    <ConsoleShell>
      <PageTitle>Orders</PageTitle>
      <Section title="Filters">
        <div className="mb-3 flex flex-wrap items-end gap-3 text-[13px]">
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-[var(--text-dim)]">Symbol</span>
            <input
              value={symbol}
              onChange={(e) => setSymbol(e.target.value)}
              className="w-32 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
              placeholder="BTCUSDT"
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-[var(--text-dim)]">Triangle</span>
            <input
              value={triangle}
              onChange={(e) => setTriangle(e.target.value)}
              className="w-40 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-[var(--text-dim)]">Cycle</span>
            <input
              value={cycle}
              onChange={(e) => setCycle(e.target.value)}
              className="w-40 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-[var(--text-dim)]">Status</span>
            <select
              value={status}
              onChange={(e) => setStatus(e.target.value)}
              className="rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
            >
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {s || "ALL"}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-[var(--text-dim)]">From</span>
            <input
              type="datetime-local"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-[var(--text-dim)]">To</span>
            <input
              type="datetime-local"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              className="rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
            />
          </label>
          {(symbol || triangle || cycle || status || from || to) && (
            <Button
              onClick={() => {
                setSymbol("");
                setTriangle("");
                setCycle("");
                setStatus("");
                setFrom("");
                setTo("");
              }}
            >
              Clear filters
            </Button>
          )}
        </div>

        {state.kind === "loading" && <Loading what="orders" />}
        {state.kind === "error" && <ErrorBox message={state.message} status={state.status} code={state.code} />}
        {state.kind === "ready" && (
          <>
            <VirtualTable
              head={["Created", "Symbol", "Leg", "Side", "Status", "Qty", "Filled", "Avg price", "Fee", "Latency", "Cycle", "Triangle", "Opportunity"]}
              empty="orders for this filter"
              rows={state.rows.map((o) => [
                fmtTime(o.created_at),
                o.symbol ?? "—",
                o.leg_no,
                o.side,
                <Badge key="s" tone={orderFillStatusTone(o.status)}>
                  {o.status}
                </Badge>,
                o.qty_requested,
                o.qty_filled,
                o.avg_price ?? "—",
                o.fee_amount ? `${o.fee_amount} ${o.fee_asset ?? ""}` : "—",
                o.latency_ms ?? "—",
                <Link key="c" href={`/fills?cycle=${encodeURIComponent(o.cycle_id)}`} className="text-[var(--accent)] underline">
                  {o.cycle_id}
                </Link>,
                o.triangle_id ? (
                  <Link key="t" href={`/triangles/${encodeURIComponent(o.triangle_id)}`} className="text-[var(--accent)] underline">
                    {o.triangle_id}
                  </Link>
                ) : (
                  "—"
                ),
                o.opportunity_id ? (
                  <Link key="o" href={`/opportunities/${encodeURIComponent(o.opportunity_id)}`} className="text-[var(--accent)] underline">
                    {o.opportunity_id}
                  </Link>
                ) : (
                  "—"
                ),
              ])}
            />
            {state.nextCursor && (
              <div className="mt-3">
                <Button onClick={loadMore} disabled={state.loadingMore}>
                  {state.loadingMore ? "Loading…" : "Load more"}
                </Button>
                {state.loadMoreError && (
                  <p className="mt-2 text-[12px] text-[var(--critical)]">
                    Load more failed
                    {state.loadMoreError.status ? ` (HTTP ${state.loadMoreError.status})` : ""}:{" "}
                    {state.loadMoreError.message}
                  </p>
                )}
              </div>
            )}
          </>
        )}
      </Section>
    </ConsoleShell>
  );
}

export default function OrdersPage() {
  return (
    <Suspense fallback={<Loading what="orders" />}>
      <OrdersPageInner />
    </Suspense>
  );
}
