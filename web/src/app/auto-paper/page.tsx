"use client";

// Auto-Paper (design §4/§7 GET /screener/auto-paper): per-rule campaign-
// style summary (alerts, executed, skipped by reason, net PnL, hit rate,
// mean lifetime) and currently open paper positions. Every execution
// here is booked through the existing paper ledger — LIVE stays
// disabled by design regardless of what a rule's auto_paper flag does.

import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  ScreenerAwait,
  pollMsFromStatus,
  signTone,
  useScreenerStatus,
} from "@/components/screener/ScreenerShared";
import { Badge, PageTitle, Section, Table, fmtTime } from "@/components/ui";

function skippedText(skipped: Record<string, number>): string {
  const entries = Object.entries(skipped).filter(([, n]) => n > 0);
  if (entries.length === 0) return "none";
  return entries.map(([reason, n]) => `${reason}: ${n}`).join(", ");
}

export default function AutoPaperPage() {
  const status = useScreenerStatus();
  const pollMs = pollMsFromStatus(status);
  const autoPaper = usePoll(() => api.screener.autoPaper(), pollMs, [pollMs]);

  return (
    <ConsoleShell active="Auto-Paper">
      <PageTitle>Auto-Paper</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Automatic execution is PAPER only, through the existing paper ledger —
        inventory held on both venues, no rebalancing simulated, funding accrued
        per interval for carry positions. LIVE execution stays disabled by
        design.
      </p>

      <Section title="Per-rule summary">
        <ScreenerAwait state={autoPaper} what="auto-paper summary">
          {(a) => (
            <Table
              head={[
                "Rule",
                "Alerts",
                "Executed",
                "Skipped (reason)",
                "Net PnL (quote)",
                "Hit rate",
                "Mean lifetime (s)",
              ]}
              empty="rule summaries (no alerts have fired yet)"
              rows={(a.summary.per_rule ?? []).map((s) => [
                s.rule_id,
                s.alerts,
                s.executed,
                skippedText(s.skipped),
                <span
                  key="pnl"
                  className={
                    signTone(s.net_pnl_quote) === "ok"
                      ? "text-[var(--ok)]"
                      : "text-[var(--critical)]"
                  }
                >
                  {s.net_pnl_quote}
                </span>,
                s.hit_rate,
                s.mean_lifetime_s,
              ])}
            />
          )}
        </ScreenerAwait>
      </Section>

      <Section title="Open positions">
        <ScreenerAwait state={autoPaper} what="open positions">
          {(a) => (
            <Table
              head={[
                "ID",
                "Rule",
                "Strategy",
                "Pair",
                "Buy → Sell",
                "Status",
                "Opened",
                "Net PnL (quote)",
              ]}
              empty="open positions"
              rows={(a.positions ?? []).map((p) => [
                p.id,
                p.rule_id ?? "—",
                p.strategy ?? "—",
                p.base && p.quote ? `${p.base}/${p.quote}` : "—",
                p.buy_venue && p.sell_venue
                  ? `${p.buy_venue} → ${p.sell_venue}`
                  : "—",
                <Badge key="st" tone={p.status === "open" ? "warn" : "dim"}>
                  {p.status ?? "—"}
                </Badge>,
                p.opened_at ? fmtTime(p.opened_at) : "—",
                p.net_pnl_quote ?? "—",
              ])}
            />
          )}
        </ScreenerAwait>
      </Section>
    </ConsoleShell>
  );
}
