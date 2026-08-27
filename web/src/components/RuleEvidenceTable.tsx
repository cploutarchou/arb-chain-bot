"use client";

// RuleEvidenceTable — the shared per-rule / per-strategy evidence summary
// (design-system.md §4.5, UX §6.7/§6.8). Built on the same table chrome as
// ui.tsx's Table, with two things Table doesn't support generically: a
// per-row disclosure sub-row for the skip-reason breakdown, and the fixed
// "sample size is always its own column" contract — n renders even at
// n=0, never folded into the hit-rate percentage.
//
// Every number here is the backend's own string, verbatim; the only
// client-side selection is "which skip reason is largest" (a max over
// already-computed counts — the same category of selection PnLSeriesChart
// already does for "worst drawdown point," not a recomputed figure).

import { Fragment, useState } from "react";
import { Badge } from "@/components/ui";
import { PlusMinusIcon } from "@/components/icons";
import { signedText, signTone } from "@/components/screener/ScreenerShared";

export interface RuleEvidenceRow {
  id: string;
  label: string;
  href?: string;
  alertsFired: number;
  executed: number;
  skipped: Record<string, number>;
  netPnlQuote: string;
  hitRate: string;
  hitRateN?: number;
  meanLifetimeS: number;
  sampleSize: number;
}

function topSkipReason(skipped: Record<string, number>): {
  text: string;
  breakdown: string;
} {
  const entries = Object.entries(skipped).filter(([, n]) => n > 0);
  if (entries.length === 0) return { text: "none", breakdown: "" };
  const top = entries.reduce((a, b) => (b[1] > a[1] ? b : a));
  const breakdown = entries.map(([reason, n]) => `${reason}: ${n}`).join(" · ");
  return { text: `${top[1]} (${top[0]})`, breakdown };
}

export function RuleEvidenceTable({
  rows,
  asOf,
  citation,
  citationHref,
}: {
  rows: RuleEvidenceRow[];
  asOf?: string;
  citation?: string;
  citationHref?: string;
}) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  if (rows.length === 0) {
    return (
      <p className="text-[13px] text-[var(--text-dim)]">
        No rule has automatic paper execution enabled yet. Turn it on from Alert
        Rules to start building a track record.
      </p>
    );
  }

  return (
    <div className="overflow-x-auto rounded border border-[var(--border)]">
      <table className="w-full border-collapse text-[13px] [font-variant-numeric:tabular-nums_slashed-zero]">
        <thead>
          <tr className="bg-[var(--bg-panel)] text-left">
            <th className="whitespace-nowrap px-3 py-2 font-medium text-[var(--text-dim)]">
              Rule / Strategy
            </th>
            <th className="whitespace-nowrap px-3 py-2 text-right font-medium text-[var(--text-dim)]">
              Alerts fired
            </th>
            <th className="whitespace-nowrap px-3 py-2 text-right font-medium text-[var(--text-dim)]">
              Executed
            </th>
            <th className="whitespace-nowrap px-3 py-2 font-medium text-[var(--text-dim)]">
              Skipped (top reason)
            </th>
            <th className="whitespace-nowrap px-3 py-2 text-right font-medium text-[var(--text-dim)]">
              Net PnL (quote)
            </th>
            <th className="whitespace-nowrap px-3 py-2 text-right font-medium text-[var(--text-dim)]">
              Hit rate
            </th>
            <th className="whitespace-nowrap px-3 py-2 text-right font-medium text-[var(--text-dim)]">
              Mean lifetime (s)
            </th>
            <th className="whitespace-nowrap px-3 py-2 text-right font-medium text-[var(--text-dim)]">
              Sample size (n)
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const isZero = r.sampleSize === 0;
            const skip = topSkipReason(r.skipped);
            const isOpen = expanded.has(r.id);
            const netDisplay = isZero ? "0.00" : signedText(r.netPnlQuote);
            const netTone = isZero
              ? "text-[var(--neu)]"
              : signTone(r.netPnlQuote) === "ok"
                ? "text-[var(--pos)]"
                : "text-[var(--neg)]";
            const thin = r.hitRateN !== undefined && r.hitRateN < 30;
            return (
              <Fragment key={r.id}>
                <tr className="border-t border-[var(--border)] hover:bg-[var(--bg-panel)]">
                  <td className="whitespace-nowrap px-3 py-1.5">
                    {r.href ? (
                      <a
                        href={r.href}
                        className="text-[var(--accent)] hover:underline"
                      >
                        {r.label}
                      </a>
                    ) : (
                      r.label
                    )}
                  </td>
                  <td className="whitespace-nowrap px-3 py-1.5 text-right">
                    {r.alertsFired}
                  </td>
                  <td className="whitespace-nowrap px-3 py-1.5 text-right">
                    {r.executed}
                  </td>
                  <td className="whitespace-nowrap px-3 py-1.5">
                    {skip.breakdown ? (
                      <button
                        type="button"
                        onClick={() => toggle(r.id)}
                        aria-expanded={isOpen}
                        className="flex items-center gap-1 text-[var(--text-dim)] hover:text-[var(--text)]"
                      >
                        <PlusMinusIcon open={isOpen} />
                        {skip.text}
                      </button>
                    ) : (
                      <span className="text-[var(--text-dim)]">
                        {skip.text}
                      </span>
                    )}
                  </td>
                  <td
                    className={`whitespace-nowrap px-3 py-1.5 text-right font-medium ${netTone}`}
                  >
                    {netDisplay}
                  </td>
                  <td className="whitespace-nowrap px-3 py-1.5 text-right">
                    {isZero ? "—" : r.hitRate}
                    {thin && (
                      <span className="ml-1">
                        <Badge tone="dim">thin</Badge>
                      </span>
                    )}
                  </td>
                  <td className="whitespace-nowrap px-3 py-1.5 text-right">
                    {isZero ? "—" : r.meanLifetimeS}
                  </td>
                  <td className="whitespace-nowrap px-3 py-1.5 text-right">
                    n = {r.sampleSize}
                  </td>
                </tr>
                {isOpen && skip.breakdown && (
                  <tr className="border-t border-[var(--border)] bg-[var(--bg-raised)]">
                    <td
                      colSpan={8}
                      className="px-3 py-1.5 text-[12px] text-[var(--text-dim)]"
                    >
                      {skip.breakdown}
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
      {(asOf || citation) && (
        <div className="flex flex-wrap items-center gap-3 border-t border-[var(--border)] px-3 py-1.5 text-[12px] text-[var(--text-dim)]">
          {asOf && <span>as of {asOf}</span>}
          {citation &&
            (citationHref ? (
              <a
                href={citationHref}
                className="font-mono text-[11px] text-[var(--accent)] hover:underline"
              >
                {citation}
              </a>
            ) : (
              <span className="font-mono text-[11px]">{citation}</span>
            ))}
        </div>
      )}
    </div>
  );
}
