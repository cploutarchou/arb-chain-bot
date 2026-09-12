"use client";

// Fills (BL-20): global, filterable fill list with the same cross-link
// chain as Orders — fill→order (inline: leg/side/status)→cycle
// (→Orders, filtered)→triangle→opportunity. "Status" filters the
// PARENT order's status; a fill has no status of its own.

import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { orderFillStatusTone } from "@/lib/tones";
import { api, ApiError, type FillListRow, type ListFilter } from "@/lib/api/client";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  Badge,
  Button,
  DecimalValue,
  ErrorBox,
  Loading,
  PageTitle,
  Section,
  VirtualTable,
  fmtTime,
} from "@/components/ui";
import { presentDecimal, presentQty } from "@/lib/decimal";

type ListState =
  | { kind: "loading" }
  | { kind: "error"; message: string; status?: number; code?: string }
  | {
      kind: "ready";
      rows: FillListRow[];
      nextCursor?: string;
      loadingMore: boolean;
      // A failed "Load more" keeps the rows already on screen (audit F7 —
      // unlike the initial load, a pagination failure must never blank a
      // table that already has real data) and reports what failed next
      // to the button, which doubles as the retry action.
      loadMoreError?: { message: string; status?: number; code?: string };
    };

const STATUSES = ["", "NEW", "FILLED", "PARTIAL", "REJECTED", "CANCELED"];

function FillsPageInner() {
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
    api.fills
      .list(filter())
      .then((page) =>
        setState({ kind: "ready", rows: page.fills ?? [], nextCursor: page.next_cursor, loadingMore: false }),
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
      const page = await api.fills.list(filter(state.nextCursor));
      setState((prev) =>
        prev.kind === "ready"
          ? { kind: "ready", rows: [...prev.rows, ...(page.fills ?? [])], nextCursor: page.next_cursor, loadingMore: false }
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
      <PageTitle>Fills</PageTitle>
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
            <span className="text-[11px] text-[var(--text-dim)]">Order status</span>
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

        {state.kind === "loading" && <Loading what="fills" />}
        {state.kind === "error" && <ErrorBox message={state.message} status={state.status} code={state.code} />}
        {state.kind === "ready" && (
          <>
            <VirtualTable
              head={["Time", "Symbol", "Leg", "Side", "Order status", "Price", "Qty", "Fee", "Book v.", "Order", "Cycle", "Triangle", "Opportunity"]}
              align={["text", "text", "num", "text", "text", "num", "num", "num", "num", "text", "text", "text", "text"]}
              label="Simulated fills"
              rowKeys={state.rows.map((f, i) => `${f.order_id}-${f.leg_no}-${i}`)}
              empty="fills for this filter"
              rows={state.rows.map((f) => [
                fmtTime(f.ts),
                f.symbol ?? "—",
                f.leg_no,
                f.side,
                <Badge key="s" tone={orderFillStatusTone(f.order_status)}>
                  {f.order_status}
                </Badge>,
                <DecimalValue key="p" d={presentQty(f.price)} />,
                <DecimalValue key="q" d={presentQty(f.qty)} />,
                <DecimalValue
                  key="fee"
                  d={presentDecimal(f.fee_amount, {
                    maxFrac: 2,
                    minFrac: 2,
                    unit: f.fee_asset ?? undefined,
                  })}
                />,
                f.book_version ?? "—",
                <span
                  key="ord"
                  className="font-mono text-[11px] text-[var(--text-dim)]"
                  title={f.order_id}
                >
                  {f.order_id.length > 12 ? `${f.order_id.slice(0, 12)}…` : f.order_id}
                </span>,
                <Link
                  key="c"
                  href={`/cycles/${encodeURIComponent(f.cycle_id)}`}
                  className="font-mono text-[11px] text-[var(--accent)] underline"
                  title={f.cycle_id}
                >
                  {f.cycle_id.length > 12 ? `${f.cycle_id.slice(0, 12)}…` : f.cycle_id}
                </Link>,
                f.triangle_id ? (
                  <Link key="t" href={`/triangles/${encodeURIComponent(f.triangle_id)}`} className="text-[var(--accent)] underline">
                    {f.triangle_id}
                  </Link>
                ) : (
                  "—"
                ),
                f.opportunity_id ? (
                  <Link key="o" href={`/opportunities/${encodeURIComponent(f.opportunity_id)}`} className="text-[var(--accent)] underline">
                    {f.opportunity_id}
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

export default function FillsPage() {
  return (
    <Suspense fallback={<Loading what="fills" />}>
      <FillsPageInner />
    </Suspense>
  );
}
