"use client";

import { useState } from "react";
import { api, type OrderRow } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, PageTitle, Section, Stat, Table, fmtTime } from "@/components/ui";

export default function PaperPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const status = usePoll(() => api.scanner.status(), 3000);
  const cycles = usePoll(() => api.paper.cycles(50), 8000);
  const [orders, setOrders] = useState<{ cycle: string; rows: OrderRow[] } | null>(null);
  const [controlErr, setControlErr] = useState("");

  const control = async (fn: () => Promise<{ running: boolean }>) => {
    setControlErr("");
    try {
      await fn();
    } catch {
      setControlErr("Control action failed (role or mode).");
    }
  };

  const showOrders = async (cycleID: string) => {
    try {
      const res = await api.paper.orders(cycleID);
      setOrders({ cycle: cycleID, rows: res.orders ?? [] });
    } catch {
      setOrders({ cycle: cycleID, rows: [] });
    }
  };

  const mayControl = can(role, "paper:control");

  return (
    <ConsoleShell active="Paper Trading">
      <PageTitle>Paper Trading</PageTitle>
      <Section title="Engine">
        <Await state={status} what="paper status">
          {(s) =>
            s.paper ? (
              <div className="max-w-4xl">
                <div className="grid grid-cols-2 gap-3 md:grid-cols-6">
                  <Stat label="State" value={s.paper.running ? "RUNNING" : "PAUSED"} tone={s.paper.running ? "ok" : "warn"} />
                  <Stat label="Active sims" value={s.paper.active_simulations} />
                  <Stat label="Received" value={s.paper.received} />
                  <Stat label="Completed" value={s.paper.completed} tone="ok" />
                  <Stat label="Failed" value={s.paper.failed} tone={s.paper.failed > 0 ? "warn" : undefined} />
                  <Stat label="Skipped" value={s.paper.skipped} />
                </div>
                <div className="mt-3 flex gap-2">
                  <Button onClick={() => control(api.paper.pause)} disabled={!mayControl} danger>
                    Pause
                  </Button>
                  <Button onClick={() => control(api.paper.resume)} disabled={!mayControl}>
                    Resume
                  </Button>
                  {!mayControl && (
                    <span className="self-center text-[12px] text-[var(--text-dim)]">
                      controls require OPERATOR
                    </span>
                  )}
                </div>
                {controlErr && <p className="mt-2 text-[12px] text-[var(--critical)]">{controlErr}</p>}
              </div>
            ) : (
              <p className="text-sm text-[var(--text-dim)]">Paper engine not running (mode is not PAPER).</p>
            )
          }
        </Await>
      </Section>
      <Section title="Cycles (persisted)">
        <Await state={cycles} what="paper cycles">
          {(c) => (
            <Table
              head={["Started", "Outcome", "PnL", "Slippage bps", "Cycle", ""]}
              empty="persisted cycles (requires ARB_DATABASE_URL)"
              rows={(c.cycles ?? []).map((row) => [
                fmtTime(row.started_at),
                <Badge key="o" tone={row.outcome === "ALL_FILLED" ? "ok" : "warn"}>{row.outcome}</Badge>,
                row.pnl_amount ? `${row.pnl_amount} ${row.pnl_asset ?? ""}` : "—",
                row.slippage_bps ?? "—",
                <span key="id" className="text-[var(--text-dim)]">{row.id}</span>,
                <Button key="b" onClick={() => showOrders(row.id)}>orders</Button>,
              ])}
            />
          )}
        </Await>
      </Section>
      {orders && (
        <Section title={`Orders — cycle ${orders.cycle}`}>
          <Table
            head={["Leg", "Market", "Side", "Status", "Requested", "Filled", "Avg price", "Fee"]}
            empty="orders for this cycle"
            rows={orders.rows.map((o) => [
              o.leg_no,
              o.market_id,
              o.side,
              <Badge key="s" tone={o.status === "FILLED" ? "ok" : "dim"}>{o.status}</Badge>,
              o.qty ?? "—",
              o.filled_qty ?? "—",
              o.avg_price ?? "—",
              o.fee ? `${o.fee} ${o.fee_asset ?? ""}` : "—",
            ])}
          />
        </Section>
      )}
    </ConsoleShell>
  );
}
