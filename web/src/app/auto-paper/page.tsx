"use client";

// Rule simulations (GET /screener/auto-paper): what the user's alert
// rules opened automatically, per rule, plus the positions still open.
//
// What the audit found (docs/design/client-area-audit-2026-09-12 §6):
// the page opened with implementation copy, then led with a 26-character
// rule identifier as the primary label, and offered no route to the rule
// it named or to the evidence behind it. Honest, but it left the reader
// with nowhere to go.
//
// Three changes, each constrained by what the backend actually returns:
//
//   * Rule **names** are joined client-side from GET /screener/rules,
//     which carries a real `name` field. The auto-paper payload itself
//     has only `rule_id` — none of PaperPosition, RuleSummary or
//     PaperBalance carries a name. So when the join finds a name it is
//     shown, and when it does not the identifier is shown as an
//     identifier, labelled and complete on demand. A name is never
//     invented, and the id is never hidden from someone who needs it.
//   * Contextual links out: to the rule's configuration and to the
//     screener evidence, which are the two places this page's numbers
//     are explained.
//   * The lead paragraph says what the page is before it says how the
//     engine is wired; the wiring stays, one section down.
//
// This ledger is deliberately separate from the triangular paper ledger
// on /paper: different API, different ledger, different metrics, and
// different controls (this one has no pause of its own — the triangular
// pause control does not reach it).

import Link from "next/link";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { presentSignedQuote } from "@/lib/decimal";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  ScreenerAwait,
  pollMsFromStatus,
  useScreenerStatus,
} from "@/components/screener/ScreenerShared";
import {
  Badge,
  Collapsible,
  DecimalValue,
  PageTitle,
  Section,
  Table,
  fmtTime,
} from "@/components/ui";
import {
  RuleEvidenceTable,
  type RuleEvidenceRow,
} from "@/components/RuleEvidenceTable";

// RuleLabel renders a rule's real name when the rules endpoint supplies
// one, and otherwise an explicitly labelled short identifier with the
// full value reachable. It never fabricates a human-readable name for a
// rule the backend only knows by id.
function RuleLabel({
  ruleId,
  names,
}: {
  ruleId: string | undefined;
  names: Map<string, string>;
}) {
  if (!ruleId) return <span className="text-[var(--text-dim)]">—</span>;
  const name = names.get(ruleId);
  if (name) {
    return (
      <span className="inline-flex flex-col gap-0.5">
        <Link
          href="/scanner-alerts"
          className="font-medium text-[var(--accent)] underline"
        >
          {name}
        </Link>
        <span
          className="font-mono text-[10px] text-[var(--text-dim)]"
          title={ruleId}
        >
          {ruleId.slice(0, 12)}…
        </span>
      </span>
    );
  }
  return (
    <span className="inline-flex flex-col gap-0.5">
      <span className="text-[12px] text-[var(--text-dim)]">
        Rule (unnamed)
      </span>
      <span className="font-mono text-[11px] text-[var(--text)]" title={ruleId}>
        {ruleId}
      </span>
    </span>
  );
}

export default function AutoPaperPage() {
  const status = useScreenerStatus();
  const pollMs = pollMsFromStatus(status);
  const autoPaper = usePoll(() => api.screener.autoPaper(), pollMs, [pollMs]);
  // The rules list is the only source of a human-readable rule name. A
  // failure here degrades to identifiers, never to a guessed name, and
  // never blocks the page.
  const rules = usePoll(() => api.screener.rules.list(), 60000);
  const names = new Map<string, string>(
    rules.kind === "ready"
      ? rules.data
          .filter((r) => r.name.trim() !== "")
          .map((r) => [r.id, r.name] as const)
      : [],
  );

  return (
    <ConsoleShell>
      <PageTitle>Rule simulations</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Trades your alert rules opened automatically, simulated only. Each rule
        below shows how often it fired, how much of that it acted on, and the
        simulated result — with the sample size, so a thin result reads as
        thin.{" "}
        <Link href="/scanner-alerts" className="text-[var(--accent)] underline">
          Configure rules
        </Link>{" "}
        ·{" "}
        <Link
          href="/screener-reports"
          className="text-[var(--accent)] underline"
        >
          Screener evidence
        </Link>
      </p>

      <Section title="Results by rule">
        <ScreenerAwait state={autoPaper} what="rule results">
          {(a) => {
            // ScreenerAutoPaperRuleSummary has no explicit `n` field;
            // hit_rate is computed over executed cycles, so `executed`
            // is the sample size — an inference from the documented
            // definition, named here so it does not read as a wire
            // field the console invented.
            const rows: RuleEvidenceRow[] = (a.summary.per_rule ?? []).map(
              (s) => ({
                id: s.rule_id,
                label: names.get(s.rule_id) ?? s.rule_id,
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
            if (rows.length === 0) {
              return (
                <p className="text-[13px] text-[var(--text-dim)]">
                  No rule has run yet. A rule simulates a trade only when it
                  fires and its own conditions are met —{" "}
                  <Link
                    href="/scanner-alerts"
                    className="text-[var(--accent)] underline"
                  >
                    create or enable a rule
                  </Link>
                  .
                </p>
              );
            }
            return (
              <>
                <RuleEvidenceTable rows={rows} />
                {rules.kind === "error" && (
                  <p className="mt-2 text-[11px] text-[var(--text-dim)]">
                    Rule names could not be loaded just now, so rules are
                    identified by their id.
                  </p>
                )}
              </>
            );
          }}
        </ScreenerAwait>
      </Section>

      <Section title="Open positions">
        <ScreenerAwait state={autoPaper} what="open positions">
          {(a) => (
            <Table
              head={[
                "Rule",
                "Strategy",
                "Pair",
                "Buy → Sell",
                "Status",
                "Opened",
                "Net result",
                "Position",
              ]}
              align={[
                "text",
                "text",
                "text",
                "text",
                "text",
                "text",
                "num",
                "text",
              ]}
              label="Open simulated positions"
              rowKeys={(a.positions ?? []).map((p) => p.id)}
              empty="open positions. A rule opens one when it fires and its conditions are met"
              rows={(a.positions ?? []).map((p) => [
                <RuleLabel key="rule" ruleId={p.rule_id} names={names} />,
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
                // tone="sign" handles zero as neutral. The previous
                // ternary sent anything that was not "ok" to the loss
                // colour, so a flat position rendered as a loss.
                <DecimalValue
                  key="pnl"
                  d={presentSignedQuote(p.net_pnl_quote, p.quote ?? "")}
                  tone="sign"
                />,
                <span
                  key="id"
                  className="font-mono text-[11px] text-[var(--text-dim)]"
                  title={p.id}
                >
                  {p.id.length > 12 ? `${p.id.slice(0, 12)}…` : p.id}
                </span>,
              ])}
            />
          )}
        </ScreenerAwait>
      </Section>

      <Collapsible
        title="How this simulation works"
        note="execution model and its limits"
      >
        <p className="max-w-2xl text-[13px] text-[var(--text-dim)]">
          Automatic execution is PAPER only, through the existing paper ledger —
          inventory held on both venues, no rebalancing simulated, funding
          accrued per interval for carry positions. LIVE execution stays
          disabled by design.
        </p>
        <p className="mt-2 max-w-2xl text-[13px] text-[var(--text-dim)]">
          This ledger is separate from the triangular simulations on{" "}
          <Link href="/paper" className="text-[var(--accent)] underline">
            Triangular simulations
          </Link>
          : a different engine, a different ledger and different result
          meanings. Pausing the triangular engine does not pause these rules —
          they have no pause control of their own.
        </p>
      </Collapsible>
    </ConsoleShell>
  );
}
