"use client";

// Perpetuals & funding monitor (design §5/§7 GET /screener/perpetuals):
// spot-vs-perp basis, current/predicted funding, annualised carry net of
// fees, in one table — spot+futures and funding-rate strategies both
// read as rows here.

import { useState } from "react";
import { api, type ScreenerPerpRow } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  NO_TRANSFER_NOTE,
  Countdown,
  ScreenerAwait,
  fmtAge,
  isStaleAge,
  pollIntervalSFromStatus,
  pollMsFromStatus,
  signTone,
  useScreenerStatus,
} from "@/components/screener/ScreenerShared";
import { PageTitle, Section, VirtualTable } from "@/components/ui";

export default function PerpetualsPage() {
  const status = useScreenerStatus();
  const pollMs = pollMsFromStatus(status);
  const pollIntervalS = pollIntervalSFromStatus(status);

  const [venue, setVenue] = useState("");
  const [base, setBase] = useState("");
  const [minCarryApr, setMinCarryApr] = useState("");

  const perps = usePoll(
    () =>
      api.screener.perpetuals({
        venue: venue.trim() || undefined,
        base: base.trim().toUpperCase() || undefined,
        min_carry_apr: minCarryApr.trim() ? Number(minCarryApr) : undefined,
        limit: 200,
      }),
    pollMs,
    [pollMs, venue, base, minCarryApr],
  );

  const rows: ScreenerPerpRow[] =
    perps.kind === "ready" ? (perps.data.rows ?? []) : [];
  const anyHoldDays = rows.find(
    (r) => r.hold_days_assumed !== undefined,
  )?.hold_days_assumed;

  return (
    <ConsoleShell active="Perpetuals">
      <PageTitle>Perpetuals & funding</PageTitle>

      <Section title="Filters">
        <div className="flex flex-wrap items-end gap-3 text-[13px]">
          <label className="flex flex-col gap-1">
            <span className="text-[12px] text-[var(--text-dim)]">Venue</span>
            <input
              value={venue}
              onChange={(e) => setVenue(e.target.value)}
              placeholder="binance"
              className="w-32 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[12px] text-[var(--text-dim)]">Base</span>
            <input
              value={base}
              onChange={(e) => setBase(e.target.value)}
              placeholder="BTC"
              className="w-28 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[12px] text-[var(--text-dim)]">
              Min carry APR (%)
            </span>
            <input
              value={minCarryApr}
              onChange={(e) => setMinCarryApr(e.target.value)}
              inputMode="decimal"
              className="w-32 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
            />
          </label>
        </div>
      </Section>

      <Section title="Basis / funding / carry">
        <ScreenerAwait state={perps} what="perpetuals">
          {() => (
            <VirtualTable
              head={[
                "Venue",
                "Base",
                "Spot mid",
                "Perp mark",
                "Perp index",
                "Basis bps",
                "Funding rate",
                "Predicted",
                "Interval (h)",
                "Next funding",
                "Carry APR gross",
                "Carry APR net",
                "Age",
              ]}
              empty="perpetuals matching these filters"
              rows={rows.map((r) => {
                const stale = isStaleAge(r.age_ms, pollIntervalS);
                const tone = signTone(r.carry_apr_net);
                return [
                  r.venue,
                  <span
                    key="b"
                    className={stale ? "text-[var(--text-dim)]" : undefined}
                  >
                    {r.base}
                  </span>,
                  r.spot_mid,
                  r.perp_mark,
                  r.perp_index,
                  r.basis_bps,
                  r.funding_rate,
                  r.predicted_funding_rate,
                  r.funding_interval_h,
                  <Countdown key="c" target={r.next_funding_at} />,
                  r.carry_apr_gross,
                  <span
                    key="n"
                    className={`font-semibold ${tone === "ok" ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
                  >
                    {r.carry_apr_net}
                  </span>,
                  fmtAge(r.age_ms),
                ];
              })}
            />
          )}
        </ScreenerAwait>
        <p className="mt-3 max-w-3xl text-[12px] text-[var(--text-dim)]">
          {anyHoldDays !== undefined
            ? `Carry APR assumes a ${anyHoldDays}-day hold (funding accrues every interval; basis convergence is not guaranteed).`
            : "Carry APR annualises the current per-interval funding rate; it is not a forecast and basis convergence is not guaranteed."}{" "}
          {NO_TRANSFER_NOTE}
        </p>
      </Section>
    </ConsoleShell>
  );
}
