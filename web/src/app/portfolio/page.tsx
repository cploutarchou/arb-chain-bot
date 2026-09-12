"use client";

import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  Await,
  DecimalValue,
  PageTitle,
  Section,
  Stat,
  Table,
} from "@/components/ui";
import {
  presentDecimal,
  presentPercentFromFraction,
  presentQuote,
  presentQty,
  presentSignedQuote,
} from "@/lib/decimal";

export default function PortfolioPage() {
  const portfolio = usePoll(() => api.portfolio(), 5000);
  const pnl = usePoll(() => api.pnl(), 5000);

  return (
    <ConsoleShell>
      <PageTitle>Balances</PageTitle>
      <Section title="Simulated balances">
        <Await state={portfolio} what="portfolio">
          {(p) => (
            <>
              <Table
                head={["Asset", "Available", "Reserved", "Equity (marked)"]}
                align={["text", "num", "num", "num"]}
                label="Simulated balances by asset"
                rowKeys={Object.keys(p.balances)}
                empty="balances"
                rows={Object.entries(p.balances).map(([asset, b]) => [
                  asset,
                  // Each figure is labelled with its own asset and never
                  // combined with another: one row per asset is the whole
                  // point, and a column total would add different monies.
                  <DecimalValue
                    key="a"
                    d={presentDecimal(b.available, { maxFrac: 2, minFrac: 2, unit: asset })}
                  />,
                  <DecimalValue
                    key="r"
                    d={presentDecimal(b.reserved, { maxFrac: 2, minFrac: 2, unit: asset })}
                  />,
                  <DecimalValue
                    key="e"
                    d={presentDecimal(p.equity[asset], { maxFrac: 2, minFrac: 2, unit: asset })}
                  />,
                ])}
              />
              {Object.keys(p.exposure).length > 0 && (
                <div className="mt-4">
                  <h3 className="mb-2 text-[12px] uppercase tracking-wider text-[var(--text-dim)]">
                    Intermediate exposure
                  </h3>
                  <Table
                    head={["Asset", "Quantity"]}
                    align={["text", "num"]}
                    label="Intermediate exposure by asset"
                    rowKeys={Object.keys(p.exposure)}
                    empty="exposure"
                    rows={Object.entries(p.exposure).map(([asset, qty]) => [
                      asset,
                      <DecimalValue key="q" d={presentQty(qty, asset)} />,
                    ])}
                  />
                </div>
              )}
              {p.unmarked.length > 0 && (
                <p className="mt-2 text-[12px] text-[var(--warn)]">
                  Unmarkable exposure (no live mark): {p.unmarked.join(", ")}
                </p>
              )}
              <div className="mt-4 grid max-w-xl grid-cols-1 gap-3 sm:grid-cols-3">
                <Stat label="Cycles" value={p.cycles} />
                <Stat label="Completed" value={p.completed} tone="ok" />
                <Stat label="Failed" value={p.failed} tone={p.failed > 0 ? "warn" : undefined} />
              </div>
            </>
          )}
        </Await>
      </Section>
      <Section title="PnL by start asset (session)">
        <Await state={pnl} what="pnl">
          {(x) => (
            <Table
              head={["Asset", "Realized", "Fees", "Daily loss", "Drawdown"]}
              align={["text", "num", "num", "num", "num"]}
              label="Simulated results by start asset"
              rowKeys={x.assets.map((a) => a.asset)}
              empty="results yet — settled cycles appear here as the engine completes them"
              rows={x.assets.map((a) => [
                a.asset,
                <DecimalValue key="r" d={presentSignedQuote(a.realized, a.asset)} tone="sign" />,
                // Costs and loss magnitudes are unsigned — both are
                // already >= 0 from the backend, so a "+" reads as a credit.
                <DecimalValue key="f" d={presentQuote(a.fees, a.asset)} />,
                <DecimalValue key="d" d={presentQuote(a.daily_loss, a.asset)} />,
                // Ratio, not money — see presentPercentFromFraction.
                <DecimalValue key="dd" d={presentPercentFromFraction(a.drawdown)} />,
              ])}
            />
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
