"use client";

import { useState } from "react";
import { api, ApiError, type OrderRow } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, ConfirmDialog, PageTitle, Section, Stat, Table, fmtTime } from "@/components/ui";

export default function PaperPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const status = usePoll(() => api.scanner.status(), 3000);
  const systemStatus = usePoll(() => api.system.status(), 10000);
  const [cyclesRefresh, setCyclesRefresh] = useState(0);
  const cycles = usePoll(() => api.paper.cycles(50), 8000, [cyclesRefresh]);
  const [orders, setOrders] = useState<{ cycle: string; rows: OrderRow[] } | null>(null);
  const [controlErr, setControlErr] = useState("");
  const [resetOpen, setResetOpen] = useState(false);
  const [resetTyped, setResetTyped] = useState("");
  const [resetMsg, setResetMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [resetBusy, setResetBusy] = useState(false);

  const control = async (fn: () => Promise<{ running: boolean }>) => {
    setControlErr("");
    try {
      await fn();
    } catch {
      setControlErr("Control action failed (role or mode).");
    }
  };

  const mayReset = can(role, "paper:reset");

  const openReset = () => {
    setResetMsg(null);
    setResetTyped("");
    setResetOpen(true);
  };

  const confirmReset = async () => {
    setResetBusy(true);
    try {
      await api.paper.reset();
      setResetMsg({ ok: true, text: "Paper session reset." });
      setResetOpen(false);
      setCyclesRefresh((n) => n + 1);
    } catch (err: unknown) {
      const text =
        err instanceof ApiError
          ? err.apiError?.code === "paper_running"
            ? "Reset requires the paper engine to be paused first. Pause it, then try again."
            : err.message
          : "Paper reset failed.";
      setResetMsg({ ok: false, text });
      setResetOpen(false);
    } finally {
      setResetBusy(false);
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
  const paperPresent = status.kind === "ready" && !!status.data.paper;
  const paperRunning = status.kind === "ready" ? status.data.paper?.running : undefined;
  const resetButtonDisabled = !mayReset || !paperPresent || paperRunning !== false;

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
              <p className="text-sm text-[var(--text-dim)]">
                {systemStatus.kind === "ready" && systemStatus.data.mode === "PAPER"
                  ? "Paper engine is not wired into this deployment profile."
                  : systemStatus.kind === "ready" ? (
                      <>
                        Paper engine is not running — current mode is <strong>{systemStatus.data.mode}</strong>.
                        Paper trading requires PAPER mode; this deployment is running{" "}
                        <strong>{systemStatus.data.mode}</strong> instead.
                      </>
                    ) : (
                      "Paper engine is not running — mode unknown (backend unreachable)."
                    )}
              </p>
            )
          }
        </Await>
      </Section>
      {mayReset && (
        <Section title="Danger zone">
          <div className="max-w-4xl rounded border border-[var(--critical)] bg-[var(--bg-panel)] p-3">
            <p className="mb-2 text-[13px] text-[var(--text-dim)]">
              Reset clears the running paper session (active simulations and in-memory counters) and
              starts a fresh one. Historical cycles already persisted are not deleted. ADMIN only, and
              only while the engine is paused.
            </p>
            <Button onClick={openReset} disabled={resetButtonDisabled} danger>
              Reset paper session…
            </Button>
            {!paperPresent ? (
              <span className="ml-2 text-[12px] text-[var(--text-dim)]">
                paper engine not running in this profile
              </span>
            ) : paperRunning !== false ? (
              <span className="ml-2 text-[12px] text-[var(--text-dim)]">
                pause the engine before resetting
              </span>
            ) : null}
            {resetMsg && (
              <p className={`mt-2 text-[12px] ${resetMsg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>
                {resetMsg.text}
              </p>
            )}
          </div>
        </Section>
      )}
      <Section title="Cycles (persisted)">
        <Await state={cycles} what="paper cycles">
          {(c) => (
            <Table
              head={["Started", "Outcome", "PnL", "Slippage bps", "Cycle", ""]}
              empty="persisted cycles yet. Persistence needs a database connection — see docs/deployment.md if this deployment doesn't have one configured"
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
      {resetOpen && (
        <ConfirmDialog
          title="Reset paper session?"
          danger
          confirmLabel={resetBusy ? "Resetting…" : "Reset session"}
          confirmDisabled={resetTyped !== "RESET" || resetBusy}
          onConfirm={confirmReset}
          onCancel={() => setResetOpen(false)}
          body={
            <div>
              <p className="mb-3">
                This clears the running paper session — active simulations and in-memory counters
                start over from zero. Historical cycles already persisted to the database are kept.
                This cannot be undone.
              </p>
              <label className="mb-1 block text-[12px] text-[var(--text-dim)]" htmlFor="paper-reset-confirm">
                Type <strong>RESET</strong> to confirm:
              </label>
              <input
                id="paper-reset-confirm"
                autoFocus
                value={resetTyped}
                onChange={(e) => setResetTyped(e.target.value)}
                spellCheck={false}
                className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
              />
            </div>
          }
        />
      )}
    </ConsoleShell>
  );
}
