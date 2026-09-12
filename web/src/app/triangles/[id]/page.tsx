"use client";

// Triangle detail (BL-26): per-leg book top / VWAP-depth preview / fee
// waterfall, recent settled cycles, and the quality score — everything
// the Triangles list links into. Live leg/book data is engine-only (this
// profile may have no engine); recent cycles/quality are store-only.
// Both are independently optional per the backend's own convention.

import { useParams } from "next/navigation";
import Link from "next/link";
import { api } from "@/lib/api/client";
import { bookAgeText } from "@/lib/format";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import { OutcomeBadge } from "@/components/OutcomeBadge";
import { Await, Badge, PageTitle, Section, Table, fmtTime } from "@/components/ui";

function bookTone(state?: string): "ok" | "warn" | "bad" | "dim" {
  if (state === "HEALTHY") return "ok";
  if (state === "SYNCING" || state === "STALE") return "warn";
  if (state === "DISCONNECTED" || state === "CORRUPTED") return "bad";
  return "dim";
}

export default function TriangleDetailPage() {
  const params = useParams<{ id: string }>();
  const id = decodeURIComponent(params.id);
  const detail = usePoll(() => api.triangles.get(id), 5000, [id]);

  return (
    <ConsoleShell>
      <PageTitle>Triangle {id}</PageTitle>
      <Await state={detail} what="triangle detail">
        {(d) => (
          <>
            {d.notes && d.notes.length > 0 && (
              <p className="mb-4 text-[12px] text-[var(--text-dim)]">{d.notes.join(" · ")}</p>
            )}

            {d.triangle ? (
              <Section title={`Legs — ${d.triangle.exchange} — starting asset ${d.triangle.starting_asset}`}>
                <Table
                  head={[
                    "Leg", "Market", "Side", "Route", "Book state", "Age (ms)", "Top bid", "Top ask",
                    "VWAP (ref size)", "Price impact bps", "Levels", "Depth exhausted", "Fee rate", "Fee source",
                  ]}
                  empty="legs"
                  label="Triangle legs"
                rowKeys={(d.triangle.legs ?? []).map((leg) => String(leg.leg_no))}
                rows={(d.triangle.legs ?? []).map((leg) => [
                    leg.leg_no,
                    leg.market,
                    leg.side,
                    `${leg.from} → ${leg.to}`,
                    leg.book_state ? <Badge key="s" tone={bookTone(leg.book_state)}>{leg.book_state}</Badge> : "—",
                    bookAgeText(leg.book_age_ms),
                    leg.top_bid ?? "—",
                    leg.top_ask ?? "—",
                    leg.vwap_price ?? "—",
                    leg.price_impact_bps ?? "—",
                    leg.levels_consumed || "—",
                    leg.depth_exhausted ? <Badge key="d" tone="warn">yes</Badge> : "no",
                    leg.fee_rate ?? "—",
                    leg.fee_source || "—",
                  ])}
                />
                {(d.triangle.legs ?? []).some((l) => l.fee_rate) && (
                  <div className="mt-3">
                    <div className="mb-1 text-[11px] uppercase tracking-wider text-[var(--text-dim)]">
                      Fee waterfall (relative fee rate per leg)
                    </div>
                    <div className="space-y-1">
                      {(d.triangle.legs ?? []).map((leg) => {
                        const rate = Number(leg.fee_rate ?? 0);
                        const maxRate = Math.max(
                          0.0001,
                          ...(d.triangle!.legs ?? []).map((l) => Number(l.fee_rate ?? 0)),
                        );
                        const pct = Math.min(100, (rate / maxRate) * 100);
                        return (
                          <div key={leg.leg_no} className="flex items-center gap-2 text-[12px]">
                            <span className="w-16 shrink-0 text-[var(--text-dim)]">leg {leg.leg_no}</span>
                            <div className="h-3 flex-1 rounded bg-[var(--bg-panel)]">
                              <div
                                className="h-3 rounded bg-[var(--warn)]"
                                style={{ width: `${pct}%` }}
                                aria-hidden
                              />
                            </div>
                            <span className="w-20 shrink-0 text-right">{leg.fee_rate ?? "—"}</span>
                          </div>
                        );
                      })}
                    </div>
                  </div>
                )}
              </Section>
            ) : (
              <Section title="Legs">
                <p className="text-sm text-[var(--text-dim)]">
                  Live leg/book/fee data unavailable — no trading engine is running in this deployment
                  profile.
                </p>
              </Section>
            )}

            <Section title="Recent settled cycles">
              <Table
                head={["Cycle", "Session", "Opportunity", "Outcome", "P&L", "Slippage bps", "Started", "Settled"]}
                empty="settled cycles recorded for this triangle"
                label="Recent cycles"
                rowKeys={(d.recent_cycles ?? []).map((c) => c.id)}
                rows={(d.recent_cycles ?? []).map((c) => [
                  <Link key="c" href={`/cycles/${encodeURIComponent(c.id)}`} className="text-[var(--accent)] underline">
                    {c.id}
                  </Link>,
                  c.session_id,
                  c.opportunity_id ? (
                    <Link key="o" href={`/opportunities/${encodeURIComponent(c.opportunity_id)}`} className="text-[var(--accent)] underline">
                      {c.opportunity_id}
                    </Link>
                  ) : (
                    "—"
                  ),
                  <OutcomeBadge key="out" code={c.outcome} />,
                  c.pnl_amount ? `${c.pnl_amount} ${c.pnl_asset ?? ""}` : "—",
                  c.slippage_bps ?? "—",
                  fmtTime(c.started_at),
                  c.settled_at ? fmtTime(c.settled_at) : "—",
                ])}
              />
            </Section>

            {d.quality ? (
              <Section title="Quality score (/100, SKILL §81 — never pure win rate)">
                <div className="mb-2 flex items-center gap-2">
                  <Badge tone={d.quality.total >= 70 ? "ok" : d.quality.total >= 40 ? "warn" : "bad"}>
                    {d.quality.total} / 100
                  </Badge>
                  <span className="text-[12px] text-[var(--text-dim)]">{d.quality.cycles} cycles considered</span>
                </div>
                <Table
                  head={["Component", "Weighted score"]}
                  empty="components"
                  label="Quality components"
                rowKeys={Object.keys(d.quality.components)}
                rows={Object.entries(d.quality.components).map(([k, v]) => [k, v])}
                />
                {d.quality.notes && d.quality.notes.length > 0 && (
                  <p className="mt-2 text-[11px] text-[var(--text-dim)]">{d.quality.notes.join(" · ")}</p>
                )}
              </Section>
            ) : (
              <Section title="Quality score">
                <p className="text-sm text-[var(--text-dim)]">
                  No quality score for this triangle in the last 30 days (needs persisted history).
                </p>
              </Section>
            )}
          </>
        )}
      </Await>
    </ConsoleShell>
  );
}
