"use client";

// Cycle detail (audit ui F13): the one timeline a review of a single
// cycle needs — the cycle row (outcome, PnL, slippage, fees, close
// reason), its legs as orders, and every fill behind them — reached
// from Paper, Triangle, Opportunity and Fills instead of dead-ending at
// an /orders?cycle= filter.

import Link from "next/link";
import { useParams } from "next/navigation";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, PageTitle, Section, Stat, Table, fmtTime } from "@/components/ui";
import { orderFillStatusTone } from "@/lib/tones";

function outcomeTone(outcome: string): "ok" | "warn" | "bad" | "dim" {
  if (outcome === "ALL_FILLED") return "ok";
  if (outcome === "ABORTED" || outcome === "FAILED") return "bad";
  return "warn";
}

export default function CycleDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const cycle = usePoll(() => api.paper.cycle(id), 10000);
  const orders = usePoll(() => api.paper.orders(id), 10000);
  const fills = usePoll(() => api.fills.list({ cycle: id, limit: 200 }), 10000);

  return (
    <ConsoleShell>
      <PageTitle>Cycle {id}</PageTitle>
      <Await state={cycle} what="cycle">
        {(c) => (
          <>
            <Section title="Cycle">
              <div className="grid max-w-3xl grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <Stat label="Outcome" value={c.outcome} tone={outcomeTone(c.outcome)} />
                <Stat
                  label={c.pnl_asset ? `PnL (${c.pnl_asset})` : "PnL"}
                  value={c.pnl_amount ?? "—"}
                />
                <Stat label="Realized PnL" value={c.realized_pnl ?? "—"} />
                <Stat label="Exposure mark" value={c.exposure_mark ?? "—"} />
                <Stat
                  label={c.pnl_asset ? `Input (${c.pnl_asset})` : "Input"}
                  value={c.input_consumed ?? "—"}
                />
                <Stat label="Final amount" value={c.final_amount ?? "—"} />
                <Stat label="Slippage (bps)" value={c.slippage_bps ?? "—"} />
                <Stat label="Started" value={fmtTime(c.started_at)} />
                <Stat label="Settled" value={c.settled_at ? fmtTime(c.settled_at) : "—"} />
                <Stat label="Session" value={c.session_id} />
                <Stat
                  label="Opportunity"
                  value={
                    c.opportunity_id ? (
                      <Link
                        href={`/opportunities/${encodeURIComponent(c.opportunity_id)}`}
                        className="text-[var(--accent)] underline"
                      >
                        {c.opportunity_id}
                      </Link>
                    ) : (
                      "unlinked"
                    )
                  }
                />
                <Stat label="Close reason" value={c.reason ?? "—"} />
              </div>
              {c.fees && Object.keys(c.fees).length > 0 && (
                <div className="mt-3">
                  <h3 className="mb-2 text-[12px] uppercase tracking-wider text-[var(--text-dim)]">
                    Fee bill
                  </h3>
                  <Table
                    head={["Asset", "Amount"]}
                    empty="fees"
                    label="Fees by asset"
              rowKeys={Object.keys(c.fees)}
              rows={Object.entries(c.fees).map(([asset, amount]) => [asset, amount])}
                  />
                </div>
              )}
            </Section>

            <Section title="Legs (orders as submitted)">
              <Await state={orders} what="cycle orders">
                {(o) => (
                  <Table
                    head={["Leg", "Market", "Side", "Status", "Qty", "Filled", "Avg price", "Fee"]}
                    empty="orders for this cycle"
                    label="Orders"
              rowKeys={(o.orders ?? []).map(
                (ord) => `${ord.leg_no}:${ord.market_id}:${ord.side}`,
              )}
              rows={(o.orders ?? []).map((ord) => [
                      ord.leg_no,
                      ord.market_id,
                      ord.side,
                      <Badge key="s" tone={orderFillStatusTone(ord.status)}>
                        {ord.status}
                      </Badge>,
                      ord.qty ?? "—",
                      ord.filled_qty ?? "—",
                      ord.avg_price ?? "—",
                      ord.fee !== undefined ? `${ord.fee} ${ord.fee_asset ?? ""}` : "—",
                    ])}
                  />
                )}
              </Await>
            </Section>

            <Section title="Fills (as executed)">
              <Await state={fills} what="cycle fills">
                {(f) => (
                  <Table
                    head={["Leg", "Order", "Price", "Qty", "Fee", "Book v"]}
                    empty="fills for this cycle"
                    label="Fills"
              rowKeys={(f.fills ?? []).map(
                (fill) => `${fill.order_id}:${fill.leg_no}:${fill.price}:${fill.qty}`,
              )}
              rows={(f.fills ?? []).map((fill) => [
                      fill.leg_no,
                      fill.order_id,
                      fill.price,
                      fill.qty,
                      fill.fee_amount !== undefined ? `${fill.fee_amount} ${fill.fee_asset ?? ""}` : "—",
                      fill.book_version ?? "—",
                    ])}
                  />
                )}
              </Await>
            </Section>
          </>
        )}
      </Await>
    </ConsoleShell>
  );
}
