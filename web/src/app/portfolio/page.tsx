"use client";

import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, PageTitle, Section, Stat, Table } from "@/components/ui";

export default function PortfolioPage() {
  const portfolio = usePoll(() => api.portfolio(), 5000);
  const pnl = usePoll(() => api.pnl(), 5000);

  return (
    <ConsoleShell active="Portfolio">
      <PageTitle>Portfolio &amp; Balances</PageTitle>
      <Section title="Virtual balances">
        <Await state={portfolio} what="portfolio">
          {(p) => (
            <>
              <Table
                head={["Asset", "Available", "Reserved", "Equity (marked)"]}
                empty="balances"
                rows={Object.entries(p.balances).map(([asset, b]) => [
                  asset,
                  b.available,
                  b.reserved,
                  p.equity[asset] ?? "—",
                ])}
              />
              {Object.keys(p.exposure).length > 0 && (
                <div className="mt-4">
                  <h3 className="mb-2 text-[12px] uppercase tracking-wider text-[var(--text-dim)]">
                    Intermediate exposure
                  </h3>
                  <Table
                    head={["Asset", "Quantity"]}
                    empty="exposure"
                    rows={Object.entries(p.exposure).map(([asset, qty]) => [asset, qty])}
                  />
                </div>
              )}
              {p.unmarked.length > 0 && (
                <p className="mt-2 text-[12px] text-[var(--warn)]">
                  Unmarkable exposure (no live mark): {p.unmarked.join(", ")}
                </p>
              )}
              <div className="mt-4 grid max-w-xl grid-cols-3 gap-3">
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
              empty="pnl rows"
              rows={x.assets.map((a) => [a.asset, a.realized, a.fees, a.daily_loss, a.drawdown])}
            />
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
