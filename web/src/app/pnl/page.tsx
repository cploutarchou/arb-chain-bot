"use client";

// PnL & Analytics (BL-19): breakdowns by exchange/triangle/asset/market/
// hour/config_version, a cumulative P&L + drawdown chart, and edge/
// slippage/latency histograms. Every aggregate is store-backed history —
// distinct from the live per-asset /pnl view on Portfolio — and every
// number rendered here is exactly what the backend computed; sample
// sizes (n) are always shown, never implied.

import { useState } from "react";
import Link from "next/link";
import { api, type PnLBreakdownBy } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, HistogramChart, PageTitle, PnLSeriesChart, Section, Table, ChipGroup} from "@/components/ui";

const WINDOWS = [24, 72, 168, 720] as const;
const BY_OPTIONS: { value: PnLBreakdownBy; label: string }[] = [
  { value: "exchange", label: "Exchange" },
  { value: "triangle", label: "Triangle" },
  { value: "asset", label: "Asset" },
  { value: "market", label: "Market" },
  { value: "hour", label: "Hour" },
  { value: "config_version", label: "Config version" },
];

function WindowPicker({ hours, onChange }: { hours: number; onChange: (h: number) => void }) {
  return (
    <ChipGroup label="PnL window" options={WINDOWS} value={hours} onChange={onChange} format={(w) => `${w}h`} />
  );
}

export default function PnLAnalyticsPage() {
  const [by, setBy] = useState<PnLBreakdownBy>("triangle");
  const [breakdownHours, setBreakdownHours] = useState(24);
  const [seriesHours, setSeriesHours] = useState(24);
  const [distHours, setDistHours] = useState(24);

  const breakdown = usePoll(() => api.pnlAnalytics.breakdown(by, breakdownHours), 15000, [by, breakdownHours]);
  const series = usePoll(() => api.pnlAnalytics.series(seriesHours), 15000, [seriesHours]);
  const distributions = usePoll(() => api.analytics.distributions(distHours), 15000, [distHours]);

  return (
    <ConsoleShell>
      <PageTitle>PnL &amp; Analytics</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Every number below is computed server-side from settled paper cycles — the console renders it,
        it never recomputes P&amp;L, drawdown, or a percentile. Sample sizes (n) are always shown.
      </p>

      <Section title="Breakdown">
        <div className="mb-3 flex flex-wrap items-center gap-2 text-[13px]">
          <span className="text-[var(--text-dim)]">By:</span>
          <ChipGroup
            label="Breakdown dimension"
            options={BY_OPTIONS}
            value={BY_OPTIONS.find((o) => o.value === by) ?? BY_OPTIONS[0]!}
            onChange={(o) => setBy(o.value)}
            format={(o) => o.label}
          />
        </div>
        <WindowPicker hours={breakdownHours} onChange={setBreakdownHours} />
        <Await state={breakdown} what="pnl breakdown">
          {(res) => {
            const rows = res.rows ?? [];
            if (rows.length === 0) {
              return (
                <p className="text-sm text-[var(--text-dim)]">
                  No settled paper cycles in this window yet. Let the{" "}
                  <Link href="/paper" className="text-[var(--accent)] underline">
                    paper engine
                  </Link>{" "}
                  run, or generate history with a{" "}
                  <Link href="/replay" className="text-[var(--accent)] underline">
                    replay
                  </Link>
                  .
                </p>
              );
            }
            const isMarket = res.by === "market";
            return (
              <>
                <Table
                  head={isMarket ? ["Key", "N", "Avg latency (ms)"] : ["Key", "N", "Net P&L"]}
                  empty="breakdown rows"
                  rows={rows.map((r) => [
                    r.key,
                    r.n,
                    isMarket ? (r.avg_latency_ms ?? "—") : (r.net_pnl ?? "—"),
                  ])}
                />
                <p className="mt-2 text-[11px] text-[var(--text-dim)]">
                  n = {res.n} cycles considered
                  {typeof res.unattributed === "number" && res.unattributed > 0
                    ? ` · ${res.unattributed} unattributed (no persisted opportunity link)`
                    : ""}
                  {res.notes && res.notes.length > 0 ? ` · ${res.notes.join(" ")}` : ""}
                </p>
              </>
            );
          }}
        </Await>
      </Section>

      <Section title="Cumulative P&L and drawdown">
        <WindowPicker hours={seriesHours} onChange={setSeriesHours} />
        <Await state={series} what="pnl series">
          {(res) => {
            const points = res.points ?? [];
            if (points.length === 0) {
              return (
                <p className="text-sm text-[var(--text-dim)]">
                  No settled paper cycles in this window yet (n = 0).
                </p>
              );
            }
            return <PnLSeriesChart points={points} />;
          }}
        </Await>
      </Section>

      <Section title="Edge / slippage / latency distributions">
        <WindowPicker hours={distHours} onChange={setDistHours} />
        <Await state={distributions} what="distributions">
          {(res) => (
            <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
              <div>
                <div className="mb-1 text-[12px] font-medium text-[var(--text-dim)]">Net edge (bps, qualified opportunities)</div>
                <HistogramChart dist={res.edge_bps} />
              </div>
              <div>
                <div className="mb-1 text-[12px] font-medium text-[var(--text-dim)]">Slippage (bps, settled cycles)</div>
                <HistogramChart dist={res.slippage_bps} />
              </div>
              <div>
                <div className="mb-1 text-[12px] font-medium text-[var(--text-dim)]">Order latency (ms)</div>
                <HistogramChart dist={res.latency_ms} />
              </div>
            </div>
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
