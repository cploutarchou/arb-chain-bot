"use client";

// Paper Trading (audit F6): a live-cycle monitor first — what is
// executing right now and where it is stuck — then the persisted
// history with the full economics (fees, duration, the backend's own
// close reason, the opportunity link). Money values are the backend's
// decimal strings rendered verbatim; the only client-side arithmetic
// is display-only (duration between two timestamps the backend sent,
// remaining = requested − filled via the exact string helper).

import Link from "next/link";
import { useState } from "react";
import { api, ApiError, type CycleRow, type OrderRow } from "@/lib/api/client";
import { usePoll, type PollState } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { subtractDecimalStr } from "@/lib/decimal";
import { fmtDurationMs } from "@/lib/feedState";
import { ConsoleShell } from "@/components/ConsoleShell";
import { PaperControl } from "@/components/PaperControl";
import { OutcomeBadge } from "@/components/OutcomeBadge";
import { ActiveCycles } from "@/components/ActiveCycles";
import { Await, Badge, Button, ConfirmDialog, PageTitle, Section, Stat, Table, fmtTime } from "@/components/ui";

function feesCell(cycle: CycleRow): string {
  const fees = cycle.fees ?? {};
  const parts = Object.entries(fees).map(([asset, v]) => `${v} ${asset}`);
  return parts.length > 0 ? parts.join(" · ") : "—";
}

function durationCell(cycle: CycleRow): string {
  if (!cycle.settled_at) return "—";
  return fmtDurationMs(
    new Date(cycle.settled_at).getTime() - new Date(cycle.started_at).getTime(),
  );
}

export default function PaperPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const status = usePoll(() => api.scanner.status(), 3000);
  const systemStatus = usePoll(() => api.system.status(), 10000);
  const [cyclesRefresh, setCyclesRefresh] = useState(0);
  const cycles = usePoll(() => api.paper.cycles(50), 8000, [cyclesRefresh]);
  // ordersCycle/ordersState split the same way usePoll does (loading/
  // error/ready) so a failed fetch renders ErrorBox instead of the empty
  // state a swallowed exception used to produce (audit F7) — a settled
  // cycle whose orders fetch 500s must never read as "no orders placed".
  const [ordersCycle, setOrdersCycle] = useState<string | null>(null);
  const [ordersState, setOrdersState] = useState<PollState<OrderRow[]>>({ kind: "loading" });
  const [ordersLoadingID, setOrdersLoadingID] = useState<string | null>(null);
  const [resetOpen, setResetOpen] = useState(false);
  const [resetTyped, setResetTyped] = useState("");
  const [resetMsg, setResetMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [resetBusy, setResetBusy] = useState(false);

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
    setOrdersCycle(cycleID);
    setOrdersState({ kind: "loading" });
    setOrdersLoadingID(cycleID);
    try {
      const res = await api.paper.orders(cycleID);
      setOrdersState({ kind: "ready", data: res.orders ?? [] });
    } catch (err: unknown) {
      setOrdersState(
        err instanceof ApiError
          ? { kind: "error", message: err.message, status: err.status, code: err.apiError?.code }
          : { kind: "error", message: "Backend unreachable" },
      );
    } finally {
      setOrdersLoadingID(null);
    }
  };

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
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-6">
                  <Stat label="State" value={s.paper.running ? "RUNNING" : "PAUSED"} tone={s.paper.running ? "ok" : "warn"} />
                  <Stat label="Active sims" value={s.paper.active_simulations} />
                  <Stat label="Received" value={s.paper.received} />
                  <Stat label="Completed" value={s.paper.completed} tone="ok" />
                  <Stat label="Failed" value={s.paper.failed} tone={s.paper.failed > 0 ? "warn" : undefined} />
                  <Stat label="Skipped" value={s.paper.skipped} />
                </div>
                <div className="mt-3">
                  <PaperControl status={status} role={role} hideStateLabel />
                </div>
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
      <Section title="Live cycles (in flight)">
        <ActiveCycles running={paperRunning} activeCount={status.kind === "ready" ? status.data.paper?.active_simulations : undefined} />
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
              head={["Started", "Outcome", "Realized PnL", "Fees", "Slip bps", "Duration", "Reason", "Cycle", ""]}
              empty="persisted cycles yet. Persistence needs a database connection — see docs/deployment.md if this deployment doesn't have one configured"
              rows={(c.cycles ?? []).map((row) => [
                fmtTime(row.started_at),
                <OutcomeBadge key="o" code={row.outcome} />,
                row.realized_pnl !== undefined ? (
                  <span key="pnl" title={`marked total ${row.pnl_amount ?? "—"} (realized ${row.realized_pnl ?? "—"} + exposure mark ${row.exposure_mark ?? "—"})`}>
                    {row.realized_pnl} {row.pnl_asset ?? ""}
                  </span>
                ) : (
                  <span key="pnl" title={`marked total ${row.pnl_amount ?? "—"}`}>
                    {row.pnl_amount ?? "—"} {row.pnl_asset ?? ""}
                  </span>
                ),
                <span key="f" title={feesCell(row)}>
                  {feesCell(row)}
                </span>,
                row.slippage_bps ?? "—",
                durationCell(row),
                row.reason ? (
                  <span key="r" className="block max-w-xs truncate text-[12px] text-[var(--text-dim)]" title={row.reason}>
                    {row.reason}
                  </span>
                ) : (
                  "—"
                ),
                <span key="id" className="text-[var(--text-dim)]">{row.id}</span>,
                <span key="act" className="flex items-center gap-2 whitespace-nowrap">
                  <Button
                    onClick={() => showOrders(row.id)}
                    disabled={ordersLoadingID === row.id}
                  >
                    {ordersLoadingID === row.id ? "loading…" : "orders"}
                  </Button>
                  {row.opportunity_id ? (
                    <Link
                      href={`/opportunities/${encodeURIComponent(row.opportunity_id)}`}
                      className="text-[12px] text-[var(--accent)] underline"
                    >
                      opportunity
                    </Link>
                  ) : (
                    <span
                      className="text-[12px] text-[var(--text-dim)]"
                      title="persisted without its opportunity row (counted as an unlinked cycle)"
                    >
                      unlinked
                    </span>
                  )}
                </span>,
              ])}
            />
          )}
        </Await>
      </Section>
      {ordersCycle && (
        <Section title={`Orders — cycle ${ordersCycle}`}>
          <Await state={ordersState} what={`orders for cycle ${ordersCycle}`}>
            {(rows) => (
              <Table
                head={["Order ID", "Leg", "Market", "Side", "Status", "Requested", "Filled", "Remaining", "Avg price", "Fee"]}
                empty="orders for this cycle"
                rows={rows.map((o) => [
                  <span key="oid" className="font-mono text-[11px] text-[var(--text-dim)]">{o.id}</span>,
                  o.leg_no,
                  o.market_id,
                  o.side,
                  <Badge key="s" tone={o.status === "FILLED" ? "ok" : "dim"}>{o.status}</Badge>,
                  o.qty ?? "—",
                  o.filled_qty ?? "—",
                  subtractDecimalStr(o.qty, o.filled_qty) ?? "—",
                  o.avg_price ?? "—",
                  o.fee ? `${o.fee} ${o.fee_asset ?? ""}` : "—",
                ])}
              />
            )}
          </Await>
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
