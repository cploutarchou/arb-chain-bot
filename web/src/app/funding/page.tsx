"use client";

// Funding monitor (design §5): a venue × base grid of current funding
// rates plus a 72h history line chart for a selected base. §7 has no
// "current rates grid" route — the grid reads the same rows the
// Perpetuals page shows (GET /screener/perpetuals already carries
// funding_rate per venue+base); only the chart calls GET /screener/funding.

import { useMemo, useState } from "react";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  ScreenerAwait,
  signTone,
  signedText,
  pollMsFromStatus,
  useScreenerStatus,
} from "@/components/screener/ScreenerShared";
import { PageTitle, Section, Table } from "@/components/ui";
import { FundingHistoryChart } from "@/components/FundingHistoryChart";

export default function FundingPage() {
  const status = useScreenerStatus();
  const pollMs = pollMsFromStatus(status);

  const perps = usePoll(() => api.screener.perpetuals({ limit: 500 }), pollMs, [
    pollMs,
  ]);
  const rows = useMemo(
    () => (perps.kind === "ready" ? (perps.data.rows ?? []) : []),
    [perps],
  );

  const venues = useMemo(
    () => [...new Set(rows.map((r) => r.venue))].sort(),
    [rows],
  );
  const bases = useMemo(
    () => [...new Set(rows.map((r) => r.base))].sort(),
    [rows],
  );
  const rateFor = useMemo(() => {
    const m = new Map<string, string>();
    for (const r of rows) m.set(`${r.venue}:${r.base}`, r.funding_rate);
    return m;
  }, [rows]);

  const [selectedBase, setSelectedBase] = useState("");
  const effectiveBase = selectedBase || bases[0] || "";

  const history = usePoll(
    () => api.screener.funding(effectiveBase, [], 72),
    pollMs,
    [pollMs, effectiveBase],
  );

  return (
    <ConsoleShell active="Funding">
      <PageTitle>Funding</PageTitle>

      <Section title="Current funding rates (venue × base)">
        <ScreenerAwait state={perps} what="funding rates">
          {() =>
            venues.length === 0 || bases.length === 0 ? (
              <p className="text-[13px] text-[var(--text-dim)]">
                No perpetuals data yet.
              </p>
            ) : (
              <Table
                head={["Base", ...venues]}
                align={["text", ...venues.map(() => "num" as const)]}
                empty="bases"
                sticky
                maxHeight={480}
                label="Funding rates by base and venue"
                rowKeys={bases}
                rows={bases.map((b) => [
                  b,
                  ...venues.map((v) => {
                    const rate = rateFor.get(`${v}:${b}`);
                    if (rate === undefined)
                      return (
                        <span key={v} className="text-[var(--text-dim)]">
                          —
                        </span>
                      );
                    const tone = signTone(rate);
                    return (
                      <span
                        key={v}
                        className={
                          tone === "ok"
                            ? "text-[var(--pos)]"
                            : "text-[var(--neg)]"
                        }
                      >
                        {signedText(rate)}
                      </span>
                    );
                  }),
                ])}
              />
            )
          }
        </ScreenerAwait>
      </Section>

      <Section title="72h funding history">
        <div className="mb-3 flex items-center gap-2 text-[13px]">
          <span className="text-[12px] text-[var(--text-dim)]">Base</span>
          <select
            value={effectiveBase}
            onChange={(e) => setSelectedBase(e.target.value)}
            className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none"
          >
            {bases.length === 0 && <option value="">—</option>}
            {bases.map((b) => (
              <option key={b} value={b}>
                {b}
              </option>
            ))}
          </select>
        </div>
        <ScreenerAwait state={history} what="funding history">
          {(h) => (
            <FundingHistoryChart
              series={(h.series ?? []).map((s) => ({
                venue: s.venue,
                points: s.points ?? [],
              }))}
            />
          )}
        </ScreenerAwait>
      </Section>
    </ConsoleShell>
  );
}
