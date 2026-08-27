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
  VenueChips,
  ageCellText,
  ageTone,
  isStaleAge,
  pollIntervalSFromStatus,
  pollMsFromStatus,
  signTone,
  signedText,
  staleCellClass,
  useScreenerStatus,
} from "@/components/screener/ScreenerShared";
import { PageTitle, Section, VirtualTable } from "@/components/ui";
import {
  FilterCard,
  FilterRow,
  NumericFilterField,
  TextFilterField,
} from "@/components/FilterCard";

function toggleIn(list: string[], v: string): string[] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
}

export default function PerpetualsPage() {
  const status = useScreenerStatus();
  const pollMs = pollMsFromStatus(status);
  const pollIntervalS = pollIntervalSFromStatus(status);

  const [venues, setVenues] = useState<string[]>([]);
  const [base, setBase] = useState("");
  const [minCarryApr, setMinCarryApr] = useState("");

  const activeFilterCount = [
    venues.length > 0,
    base.trim() !== "",
    minCarryApr.trim() !== "",
  ].filter(Boolean).length;

  const perps = usePoll(
    () =>
      api.screener.perpetuals({
        base: base.trim().toUpperCase() || undefined,
        min_carry_apr: minCarryApr.trim() ? Number(minCarryApr) : undefined,
        limit: 200,
      }),
    pollMs,
    [pollMs, base, minCarryApr],
  );

  const allRows: ScreenerPerpRow[] =
    perps.kind === "ready" ? (perps.data.rows ?? []) : [];
  // Venue is a client-side display filter (the wire contract's `venue`
  // param is a single value, §7) — same "not a new backend param"
  // convention as Screener's bases_deny.
  const rows = venues.length
    ? allRows.filter((r) => venues.includes(r.venue))
    : allRows;
  const anyHoldDays = rows.find(
    (r) => r.hold_days_assumed !== undefined,
  )?.hold_days_assumed;

  return (
    <ConsoleShell active="Perpetuals">
      <PageTitle>Perpetuals & funding</PageTitle>

      <FilterCard activeCount={activeFilterCount}>
        <FilterRow label="Venues">
          <VenueChips
            selected={venues}
            onToggle={(v) => setVenues((prev) => toggleIn(prev, v))}
          />
        </FilterRow>
        <div className="flex flex-wrap items-end gap-3">
          <TextFilterField
            label="Base"
            value={base}
            onChange={setBase}
            placeholder="BTC"
            width="w-28"
          />
          <NumericFilterField
            label="Min carry APR"
            value={minCarryApr}
            onChange={setMinCarryApr}
            unit="% APR"
            width="w-32"
          />
        </div>
      </FilterCard>

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
              align={[
                "text",
                "text",
                "num",
                "num",
                "num",
                "num",
                "num",
                "num",
                "num",
                "text",
                "num",
                "num",
                "text",
              ]}
              empty={`perpetuals matching these filters — 0 of ${allRows.length} contracts qualify`}
              rows={rows.map((r) => {
                const stale = isStaleAge(r.age_ms, pollIntervalS);
                const dim = staleCellClass(stale);
                const tone = signTone(r.carry_apr_net);
                const at = ageTone(r.age_ms, pollIntervalS);
                return [
                  <span key="v" className={dim}>
                    {r.venue}
                  </span>,
                  <span key="b" className={dim}>
                    {r.base}
                  </span>,
                  <span key="sm" className={dim}>
                    {r.spot_mid}
                  </span>,
                  <span key="pm" className={dim}>
                    {r.perp_mark}
                  </span>,
                  <span key="pi" className={dim}>
                    {r.perp_index}
                  </span>,
                  <span key="ba" className={dim}>
                    {r.basis_bps}
                  </span>,
                  <span key="fr" className={dim}>
                    {r.funding_rate}
                  </span>,
                  <span key="pf" className={dim}>
                    {r.predicted_funding_rate}
                  </span>,
                  <span key="in" className={dim}>
                    {r.funding_interval_h}
                  </span>,
                  <Countdown key="c" target={r.next_funding_at} />,
                  <span key="cg" className={dim}>
                    {r.carry_apr_gross}
                  </span>,
                  <span
                    key="n"
                    className={`font-semibold ${dim} ${tone === "ok" ? "text-[var(--pos)]" : "text-[var(--neg)]"}`}
                  >
                    {signedText(r.carry_apr_net)}
                  </span>,
                  <span
                    key="age"
                    className={
                      at === "bad"
                        ? "text-[var(--critical)]"
                        : at === "warn"
                          ? "text-[var(--warn)]"
                          : "text-[var(--text-dim)]"
                    }
                  >
                    {ageCellText(r.age_ms, pollIntervalS)}
                  </span>,
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
