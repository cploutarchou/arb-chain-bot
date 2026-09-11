"use client";

// Risk Center. Since the breaker-acknowledgement endpoint landed, this
// is also where an ADMIN closes a breaker that the policies
// deliberately leave OPEN (daily_loss, drawdown, slippage,
// simulation_inconsistency — docs/risk.md §5: automatic resume is off):
// the close control sits beside the breaker's own reason, requires
// typing the breaker's name, and reports the backend's message verbatim
// on failure. Nothing here can change a limit — that stays on the
// versioned strategy config.

import { useState } from "react";
import Link from "next/link";
import { api, ApiError, type HealthView, type PortfolioView, type PnLView, type RiskView } from "@/lib/api/client";
import { reasonText } from "@/lib/reasons";
import { ALL_FIELDS } from "@/lib/strategyFields";
import { usePoll, type PollState } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { useToast } from "@/components/Toast";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, PageTitle, Section, Stat, Table, fmtTime, ChipGroup} from "@/components/ui";

const WINDOWS = [24, 72, 168, 720] as const;

function BreakerCloseDialog({
  breaker,
  onClose,
  onClosed,
}: {
  breaker: { Name: string; Scope: string; State: string; Reason: string };
  onClose: () => void;
  onClosed: () => void;
}) {
  const toast = useToast();
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);

  const run = async () => {
    setBusy(true);
    try {
      const res = await api.riskBreakers.close(breaker.Name, breaker.Scope);
      toast.push({
        tone: "ok",
        text: `Breaker ${res.name} is ${res.state} — qualification resumes on the next evaluation.`,
      });
      onClosed();
      onClose();
    } catch (err: unknown) {
      toast.push({
        tone: "bad",
        text:
          err instanceof ApiError
            ? `Close failed (HTTP ${err.status}): ${err.message}`
            : "Close failed: backend unreachable.",
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="dialog"
      aria-modal="true"
      aria-label={`Close breaker ${breaker.Name}`}
    >
      <div className="w-full max-w-md rounded border border-[var(--critical)] bg-[var(--bg-panel)] p-4">
        <h3 className="mb-2 text-sm font-semibold text-[var(--critical)]">
          Close breaker {breaker.Name}?
        </h3>
        <p className="mb-2 text-[13px] text-[var(--text-dim)]">
          It is {breaker.State} because: {breaker.Reason || "(no reason recorded)"}. Closing it
          resumes qualification on the next evaluation even though the condition that opened it
          is not proven gone — the loss and drawdown breakers re-open immediately at their
          limits, but read the reason first.
        </p>
        <label
          className="mb-1 block text-[12px] text-[var(--text-dim)]"
          htmlFor="breaker-close-confirm"
        >
          Type the breaker name (<strong>{breaker.Name}</strong>) to confirm:
        </label>
        <input
          id="breaker-close-confirm"
          autoFocus
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          spellCheck={false}
          className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--critical)]"
        />
        <div className="mt-3 flex justify-end gap-2">
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={run} danger disabled={typed !== breaker.Name || busy}>
            {busy ? "Closing…" : `Close ${breaker.Name}`}
          </Button>
        </div>
      </div>
    </div>
  );
}

function BreakersTable({
  view,
  role,
  onClosed,
}: {
  view: RiskView;
  role: string | undefined;
  onClosed: () => void;
}) {
  const [closing, setClosing] = useState<{ Name: string; Scope: string; State: string; Reason: string } | null>(null);
  const mayClose = can(role, "risk:config");
  const breakers = view.breakers ?? [];

  return (
    <>
      <Table
        head={["Name", "Scope", "State", "Reason", ""]}
        empty="registered breakers"
        rows={breakers.map((b) => [
          b.Name,
          b.Scope || "global",
          <Badge key="s" tone={b.State === "OPEN" ? "bad" : b.State === "HALF_OPEN" ? "warn" : "ok"}>
            {b.State}
          </Badge>,
          b.Reason || "—",
          mayClose && b.State !== "CLOSED" ? (
            <Button key="c" onClick={() => setClosing(b)}>
              Close…
            </Button>
          ) : (
            "—"
          ),
        ])}
      />
      {mayClose && breakers.length > 0 && breakers.every((b) => b.State === "CLOSED") && (
        <p className="mt-2 text-[11px] text-[var(--text-dim)]">
          Breakers that trip on loss, drawdown, slippage or ledger inconsistency stay OPEN
          until an operator closes them here — automatic resume is deliberately off.
        </p>
      )}
      {closing && (
        <BreakerCloseDialog breaker={closing} onClose={() => setClosing(null)} onClosed={onClosed} />
      )}
    </>
  );
}

export default function RiskPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const [riskRefresh, setRiskRefresh] = useState(0);
  const risk = usePoll(() => api.risk(), 5000, [riskRefresh]);
  const [hours, setHours] = useState<number>(24);
  const events = usePoll(() => api.riskEvents.list(hours, 200), 10000, [hours]);
  // F12: the exposure and budget figures the strip pairs with the
  // limits — the same endpoints the Portfolio page reads, composed for
  // display only (nothing here recomputes a limit).
  const portfolio = usePoll(() => api.portfolio(), 5000);
  const pnl = usePoll(() => api.pnl(), 5000);
  const health = usePoll(() => api.system.health(), 5000);

  return (
    <ConsoleShell active="Risk Center">
      <PageTitle>Risk Center</PageTitle>
      <RiskTopStrip risk={risk} portfolio={portfolio} pnl={pnl} health={health} />
      <Await state={risk} what="risk state">
        {(r) => (
          <>
            <Section title={`Effective limits (config v${r.config_version ?? "?"}) — deterministic engine; nothing overrides it`}>
              <Table
                head={["Limit", "Value"]}
                empty="limits"
                rows={Object.entries(r.limits ?? {}).map(([k, v]) => [limitLabel(k), String(v)])}
              />
            </Section>
            <Section title="Circuit breakers">
              <BreakersTable view={r} role={role} onClosed={() => setRiskRefresh((n) => n + 1)} />
            </Section>
            <Section title="Rejection reasons (session)">
              <Table
                head={["Reason code", "Count"]}
                empty="rejections recorded"
                rows={Object.entries(r.reject_reason_counts ?? {})
                  .sort((a, b) => b[1] - a[1])
                  .map(([code, n]) => [
                    <Link key="c" href="/opportunities?status=REJECTED" className="text-[var(--accent)] underline" title={`${code} — filtered opportunities`}>
                      {reasonText(code)}
                    </Link>,
                    n,
                  ])}
              />
            </Section>
          </>
        )}
      </Await>

      <Section title="Risk event timeline (persisted — survives a restart, unlike the session counter above)">
        <ChipGroup label="Event window" options={WINDOWS} value={hours} onChange={setHours} format={(w) => `${w}h`} />
        <Await state={events} what="risk events">
          {(res) => (
            <>
              <Table
                head={["Time", "Kind", "Subject", "Limit", "Observed", "Threshold", "Action", "Breaker state", "Correlation"]}
                empty="risk events in this window"
                sticky
                maxHeight={480}
                rows={(res.events ?? []).map((ev) => [
                  fmtTime(ev.ts),
                  <Badge key="k" tone={ev.kind === "breaker_transition" ? "warn" : "dim"}>
                    {ev.kind}
                  </Badge>,
                  ev.subject ?? "—",
                  ev.limit_name ?? "—",
                  ev.observed ?? "—",
                  ev.threshold ?? "—",
                  ev.action ?? "—",
                  ev.breaker_state ? (
                    <Badge
                      key="b"
                      tone={ev.breaker_state === "OPEN" ? "bad" : ev.breaker_state === "HALF_OPEN" ? "warn" : "ok"}
                    >
                      {ev.breaker_state}
                    </Badge>
                  ) : (
                    "—"
                  ),
                  ev.correlation_id ?? "—",
                ])}
              />
              <p className="mt-2 text-[11px] text-[var(--text-dim)]">n = {res.n} events</p>
            </>
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}

// limitLabel shares the strategy form's field registry with the Risk
// Center (F11): a limit reads as "Max slippage (bps)", not
// max_slippage_bps. Unknown keys pass through unchanged — the backend
// can grow fields the form has not catalogued yet.
function limitLabel(key: string): string {
  const field = ALL_FIELDS.find((f) => f.key === key || f.path.endsWith(`.${key}`));
  if (!field) return key;
  const unit = field.unit ?? field.unitAsset;
  return unit ? `${field.label} (${unit})` : field.label;
}
// RiskTopStrip (audit ui F12): the five numbers an operator needs
// before touching a limit — breakers open, daily loss against its
// budget per asset, drawdown against its max, exposure that cannot be
// marked, and books not HEALTHY. Composition is display-only; each
// figure is the backend's own, paired with its configured bound.
function RiskTopStrip({
  risk,
  portfolio,
  pnl,
  health,
}: {
  risk: PollState<RiskView>;
  portfolio: PollState<PortfolioView>;
  pnl: PollState<PnLView>;
  health: PollState<HealthView>;
}) {
  const r = risk.kind === "ready" || risk.kind === "error" ? risk.data : undefined;
  const limits = r?.limits ?? {};
  const maxDailyLoss = String(limits["max_daily_loss"] ?? "");
  const maxDrawdownPct = limits["max_drawdown"] !== undefined ? `${Number(limits["max_drawdown"]) * 100}%` : "";
  const open = (r?.breakers ?? []).filter((b) => b.State === "OPEN");

  const p = portfolio.kind === "ready" || portfolio.kind === "error" ? portfolio.data : undefined;
  const x = pnl.kind === "ready" || pnl.kind === "error" ? pnl.data : undefined;
  const h = health.kind === "ready" || health.kind === "error" ? health.data : undefined;
  const staleBooks = (h?.books ?? []).filter((b) => b.state !== "HEALTHY");

  const dailyLoss = (x?.assets ?? [])
    .filter((a) => a.daily_loss && a.daily_loss !== "0")
    .map((a) => `${a.daily_loss} ${a.asset}`);
  const drawdown = (x?.assets ?? [])
    .filter((a) => a.drawdown && a.drawdown !== "0")
    .map((a) => `${(Number(a.drawdown) * 100).toFixed(1)}% ${a.asset}`);

  return (
    <Section title="At a glance">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <Stat
          label="Breakers open"
          value={open.length > 0 ? open.map((b) => b.Name).join(", ") : "none"}
          tone={open.length > 0 ? "bad" : "ok"}
        />
        <Stat
          label={maxDailyLoss ? `Daily loss (max ${maxDailyLoss})` : "Daily loss"}
          value={dailyLoss.length > 0 ? dailyLoss.join(", ") : "none"}
          tone={dailyLoss.length > 0 ? "warn" : "ok"}
        />
        <Stat
          label={maxDrawdownPct ? `Drawdown (max ${maxDrawdownPct})` : "Drawdown"}
          value={drawdown.length > 0 ? drawdown.join(", ") : "none"}
          tone={drawdown.length > 0 ? "warn" : "ok"}
        />
        <Stat
          label="Unmarkable exposure"
          value={p && p.unmarked.length > 0 ? p.unmarked.join(", ") : "none"}
          tone={p && p.unmarked.length > 0 ? "warn" : "ok"}
        />
        <Stat
          label="Books not HEALTHY"
          value={staleBooks.length > 0 ? `${staleBooks.length} (${staleBooks[0]?.market}…)` : "none"}
          tone={staleBooks.length > 0 ? "warn" : "ok"}
        />
      </div>
      <p className="mt-2 text-[11px] text-[var(--text-dim)]">
        Rejection reasons below link to the opportunities they filtered.
      </p>
    </Section>
  );
}
