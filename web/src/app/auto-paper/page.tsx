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
  signedText,
  useScreenerStatus,
} from "@/components/screener/ScreenerShared";
import { Badge, PageTitle, Section, Table, fmtTime } from "@/components/ui";
import {
  RuleEvidenceTable,
  type RuleEvidenceRow,
} from "@/components/RuleEvidenceTable";

export default function AutoPaperPage() {
  const status = useScreenerStatus();
  const pollMs = pollMsFromStatus(status);
  const autoPaper = usePoll(() => api.screener.autoPaper(), pollMs, [pollMs]);

  return (
    <ConsoleShell>
      <PageTitle>Auto-Paper</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Automatic execution is PAPER only, through the existing paper ledger —
        inventory held on both venues, no rebalancing simulated, funding accrued
        per interval for carry positions. LIVE execution stays disabled by
        design.
      </p>

      <Section title="Per-rule summary">
        <ScreenerAwait state={autoPaper} what="auto-paper summary">
          {(a) => {
            // ScreenerAutoPaperRuleSummary (§7) has no explicit `n` field;
            // hit_rate is computed over executed cycles, so `executed` is
            // the sample size §4.5 requires as its own column — an
            // inference, not a wire field, named here so it doesn't read
            // as a backend value the frontend invented independently.
            const rows: RuleEvidenceRow[] = (a.summary.per_rule ?? []).map(
              (s) => ({
                id: s.rule_id,
                label: s.rule_id,
                alertsFired: s.alerts,
                executed: s.executed,
                skipped: s.skipped,
                netPnlQuote: s.net_pnl_quote,
                hitRate: s.hit_rate,
                hitRateN: s.executed,
                meanLifetimeS: s.mean_lifetime_s,
                sampleSize: s.executed,
              }),
            );
            return <RuleEvidenceTable rows={rows} />;
          }}
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
              align={[
                "text",
                "text",
                "text",
                "text",
                "text",
                "text",
                "text",
                "num",
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
                <Badge
                  key="st"
                  tone={
                    p.status === "open"
                      ? "warn"
                      : p.status === "stopped (maintenance margin)"
                        ? "warn"
                        : "dim"
                  }
                >
                  {p.status ?? "—"}
                </Badge>,
                p.opened_at ? fmtTime(p.opened_at) : "—",
                p.net_pnl_quote !== undefined ? (
                  <span
                    key="pnl"
                    className={
                      signTone(p.net_pnl_quote) === "ok"
                        ? "text-[var(--pos)]"
                        : "text-[var(--neg)]"
                    }
                  >
                    {signedText(p.net_pnl_quote)}
                  </span>
                ) : (
                  "—"
                ),
              ])}
            />
          )}
        </ScreenerAwait>
      </Section>
    </ConsoleShell>
  );
}
