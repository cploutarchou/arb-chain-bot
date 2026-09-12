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
import {
  presentDecimal,
  presentQty,
  presentSignedQuote,
  subtractDecimalStr,
} from "@/lib/decimal";
import { fmtDurationMs } from "@/lib/feedState";
import { ConsoleShell } from "@/components/ConsoleShell";
import { PaperControl } from "@/components/PaperControl";
import { OutcomeBadge } from "@/components/OutcomeBadge";
import { ActiveCycles } from "@/components/ActiveCycles";
import {
  Await,
  Badge,
  Button,
  Collapsible,
  ConfirmDialog,
  DecimalValue,
  PageTitle,
  Section,
  Stat,
  Table,
  fmtTime,
} from "@/components/ui";

// feesCell lists the fee paid in each asset, bounded for display and
// never combined: a cycle can pay fees in more than one asset, and
// adding them would be adding different monies.
function FeesCell({ cycle }: { cycle: CycleRow }) {
  const entries = Object.entries(cycle.fees ?? {});
  if (entries.length === 0) return <span className="text-[var(--text-dim)]">—</span>;
  return (
    <span className="inline-flex flex-col items-end gap-0.5">
      {entries.map(([asset, v]) => (
        <DecimalValue key={asset} d={presentDecimal(v, { maxFrac: 2, minFrac: 2, unit: asset })} />
      ))}
    </span>
  );
}

// feesExact is the same list at full precision, for the row tooltip.
function feesExact(cycle: CycleRow): string {
  const parts = Object.entries(cycle.fees ?? {}).map(([asset, v]) => `${v} ${asset}`);
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
  // Whether persistence is configured at all. The history empty state
  // used to claim "Persistence needs a database connection" regardless,
  // which contradicted Overview reporting the database as connected
  // (audit §5). This distinguishes a healthy-but-empty history from an
  // unconfigured deployment; a configured-but-failing database is not
  // representable in this boolean, so the failing request reports itself
  // through its own error state rather than this sentence.
  const recordings = usePoll(() => api.recordings.list(), 30000);
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
      setOrdersState({ kind: "ready", data: res.orders ?? [], lastOkAt: Date.now() });
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
      <Section title="Triangular simulation engine">
        <Await state={status} what="paper status">
          {(s) =>
            s.paper ? (
              <div className="max-w-4xl">
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-6">
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
      <Section title="Running now">
        <ActiveCycles running={paperRunning} />
      </Section>
      <Section title="Simulation history">
        <Await state={cycles} what="simulation history">
          {(c) => (
            <Table
              head={["Started", "Outcome", "Realized PnL", "Fees", "Slip bps", "Duration", "Reason", "Cycle", ""]}
              label="Simulation history"
              rowKeys={(c.cycles ?? []).map((row) => row.id)}
              // The empty state reports the deployment's actual
              // persistence state instead of always blaming the
              // database: a healthy, configured deployment that simply
              // has not settled a cycle yet says exactly that.
              empty={
                recordings.kind === "ready" && !recordings.data.persistence
                  ? "simulated cycles — this deployment has no database configured, so nothing is kept between restarts. See docs/deployment.md"
                  : recordings.kind === "error"
                    ? "simulated cycles to show. Whether history is being kept could not be checked just now"
                    : "simulated cycles yet. Settled cycles appear here as the engine completes them"
              }
              rows={(c.cycles ?? []).map((row) => [
                fmtTime(row.started_at),
                <OutcomeBadge key="o" code={row.outcome} />,
                row.realized_pnl !== undefined ? (
                  <span key="pnl" title={`marked total ${row.pnl_amount ?? "—"} (realized ${row.realized_pnl ?? "—"} + exposure mark ${row.exposure_mark ?? "—"})`}>
                    <DecimalValue
                      d={presentSignedQuote(row.realized_pnl, row.pnl_asset ?? "")}
                      tone="sign"
                    />
                  </span>
                ) : (
                  <span key="pnl" title={`marked total ${row.pnl_amount ?? "—"}`}>
                    <DecimalValue
                      d={presentSignedQuote(row.pnl_amount, row.pnl_asset ?? "")}
                      tone="sign"
                    />
                  </span>
                ),
                <span key="f" title={feesExact(row)}>
                  <FeesCell cycle={row} />
                </span>,
                <DecimalValue
                  key="slip"
                  d={presentDecimal(row.slippage_bps, { maxFrac: 2, minFrac: 2, unit: "bps" })}
                />,
                durationCell(row),
                row.reason ? (
                  <span key="r" className="block max-w-xs truncate text-[12px] text-[var(--text-dim)]" title={row.reason}>
                    {row.reason}
                  </span>
                ) : (
                  "—"
                ),
                // The full identifier stays available for inspection —
                // it is what an operator quotes in a bug report — but it
                // is not the row's headline: the first segment plus a
                // title carrying the whole value reads as an identifier
                // rather than competing with the economics beside it.
                <span
                  key="id"
                  className="font-mono text-[11px] text-[var(--text-dim)]"
                  title={row.id}
                >
                  {row.id.length > 10 ? `${row.id.slice(0, 10)}…` : row.id}
                </span>,
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
                label={`Orders for cycle ${ordersCycle}`}
                rowKeys={rows.map((o) => o.id)}
                rows={rows.map((o) => [
                  <span key="oid" className="font-mono text-[11px] text-[var(--text-dim)]">{o.id}</span>,
                  o.leg_no,
                  o.market_id,
                  o.side,
                  <Badge key="s" tone={o.status === "FILLED" ? "ok" : "dim"}>{o.status}</Badge>,
                  <DecimalValue key="q" d={presentQty(o.qty)} />,
                  <DecimalValue key="fq" d={presentQty(o.filled_qty)} />,
                  <DecimalValue key="rq" d={presentQty(subtractDecimalStr(o.qty, o.filled_qty))} />,
                  <DecimalValue key="ap" d={presentQty(o.avg_price)} />,
                  <DecimalValue
                    key="fee"
                    d={presentDecimal(o.fee, { maxFrac: 2, minFrac: 2, unit: o.fee_asset ?? undefined })}
                  />,
                ])}
              />
            )}
          </Await>
        </Section>
      )}
      {mayReset && (
        // Session reset moves below the normal work and starts closed.
        // It was a prominent red panel sitting between the live monitor
        // and the history (audit §5), so the most destructive control on
        // the page had the strongest visual pull. Every safeguard is
        // unchanged: ADMIN only, the engine must be paused first,
        // type-to-confirm RESET, and the backend enforces all three.
        <Collapsible
          title="Reset this simulation session"
          note="clears the running session — ADMIN only, engine must be paused"
        >
          <div className="max-w-4xl rounded border border-[var(--critical)] bg-[var(--bg-panel)] p-3">
            <p className="mb-2 text-[13px] text-[var(--text-dim)]">
              Reset clears the running paper session (active simulations and
              in-session counters) and starts a fresh one. Simulation history
              already saved is not deleted. ADMIN only, and only while the
              engine is paused.
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
        </Collapsible>
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
