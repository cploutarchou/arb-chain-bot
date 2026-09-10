"use client";

// Opportunity detail (BL-27): the full economics row, the persisted risk
// decision/reason, the book versions the quote was computed from, and
// the simulation result (settled paper cycle) when one exists.

import { useParams } from "next/navigation";
import Link from "next/link";
import { api } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { signTone, signedText } from "@/lib/decimal";
import { ConsoleShell } from "@/components/ConsoleShell";
import { OutcomeBadge } from "@/components/OutcomeBadge";
import { Await, Badge, PageTitle, Section, Stat, Table, fmtTime } from "@/components/ui";

export default function OpportunityDetailPage() {
  const params = useParams<{ id: string }>();
  const id = decodeURIComponent(params.id);
  const detail = usePoll(() => api.opportunities.get(id), 5000, [id]);

  return (
    <ConsoleShell active="Opportunities">
      <PageTitle>Opportunity {id}</PageTitle>
      <Await state={detail} what="opportunity detail">
        {(d) => (
          <>
            {d.notes && d.notes.length > 0 && (
              <p className="mb-4 text-[12px] text-[var(--text-dim)]">{d.notes.join(" · ")}</p>
            )}

            <Section title="Economics">
              <p className="mb-3 max-w-2xl text-[12px] text-[var(--text-dim)]">
                Every figure below already has trading fees removed. The two
                labelled &ldquo;before buffers&rdquo; still have the execution buffer
                (a latency/risk margin) in them; the Net figures have that buffer
                taken out too — the Net-to-before-buffers gap is buffer, not fees.
              </p>
              <div className="grid max-w-4xl grid-cols-2 gap-3 md:grid-cols-4">
                <Stat
                  label="Status"
                  value={
                    <Badge tone={d.status === "QUALIFIED" ? "ok" : d.status === "REJECTED" ? "dim" : "warn"}>
                      {d.status}
                    </Badge>
                  }
                />
                <Stat
                  label="Triangle"
                  value={
                    <Link href={`/triangles/${encodeURIComponent(d.triangle_id)}`} className="text-[var(--accent)] underline">
                      {d.triangle_id}
                    </Link>
                  }
                />
                <Stat label="Exchange" value={d.exchange_id} />
                <Stat label="Starting" value={`${d.starting_amount} ${d.starting_asset}`} />
                <Stat
                  label="Net profit (after buffers)"
                  value={signedText(d.net_profit)}
                  tone={signTone(d.net_profit)}
                />
                <Stat
                  label="Net return bps (after buffers)"
                  value={signedText(d.net_return_bps)}
                  tone={signTone(d.net_return_bps)}
                />
                <Stat label="Estimated final (after fees & buffers)" value={d.estimated_final_amount ?? "—"} />
                <Stat label="Final (after fees, before buffers)" value={d.gross_final_amount ?? "—"} tone="dim" />
                <Stat
                  label="Profit (after fees, before buffers)"
                  value={signedText(d.gross_profit)}
                  tone="dim"
                />
                <Stat
                  label="Return bps (after fees, before buffers)"
                  value={signedText(d.gross_return_bps)}
                  tone="dim"
                />
                <Stat label="Data quality" value={d.data_quality ?? "—"} />
                <Stat label="Config version" value={d.config_version ? `v${d.config_version}` : "—"} />
                <Stat label="Detected" value={fmtTime(d.detected_at)} />
                <Stat label="Expires" value={d.expires_at ? fmtTime(d.expires_at) : "—"} />
                <Stat label="Decided" value={d.decided_at ? fmtTime(d.decided_at) : "—"} />
              </div>
            </Section>

            <Section title="Legs (as evaluated)">
              <LegsTable legs={d.legs} />
            </Section>

            <Section title="Risk decision">
              {d.decision ? (
                <div className="max-w-2xl space-y-2 text-[13px]">
                  <div className="flex items-center gap-2">
                    <Badge tone={d.decision.allowed ? "ok" : "bad"}>{d.decision.allowed ? "ALLOWED" : "BLOCKED"}</Badge>
                    {d.decision.reason_code && <span className="text-[var(--text-dim)]">{d.decision.reason_code}</span>}
                    {d.decision.config_version ? (
                      <span className="text-[var(--text-dim)]">config v{d.decision.config_version}</span>
                    ) : null}
                    {d.decision.legacy && <Badge tone="dim">reconstructed from legacy row</Badge>}
                  </div>
                  {d.decision.checks !== undefined && (
                    <pre className="max-h-64 overflow-auto rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3 text-[12px]">
                      {JSON.stringify(d.decision.checks, null, 2)}
                    </pre>
                  )}
                </div>
              ) : (
                <p className="text-sm text-[var(--text-dim)]">No persisted risk decision for this opportunity.</p>
              )}
            </Section>

            <Section title="Book versions used">
              {d.book_versions && d.book_versions.length > 0 ? (
                <p className="text-[13px]">{d.book_versions.join(", ")}</p>
              ) : (
                <p className="text-sm text-[var(--text-dim)]">Not recorded for this opportunity.</p>
              )}
            </Section>

            <Section title="Simulation result">
              {d.simulation ? (
                <div className="max-w-4xl">
                  <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                    <Stat
                      label="Cycle"
                      value={
                        <Link href={`/cycles/${encodeURIComponent(d.simulation.cycle_id)}`} className="text-[var(--accent)] underline">
                          {d.simulation.cycle_id}
                        </Link>
                      }
                    />
                    <Stat
                      label="Outcome"
                      value={<OutcomeBadge code={d.simulation.outcome} showDescription />}
                    />
                    <Stat label="P&L" value={d.simulation.pnl_amount ? `${d.simulation.pnl_amount} ${d.simulation.pnl_asset ?? ""}` : "—"} />
                    <Stat label="Slippage bps" value={d.simulation.slippage_bps ?? "—"} />
                    <Stat label="Started" value={fmtTime(d.simulation.started_at)} />
                    <Stat label="Settled" value={d.simulation.settled_at ? fmtTime(d.simulation.settled_at) : "—"} />
                  </div>
                  {(d.simulation.fees !== undefined || d.simulation.exposure !== undefined) && (
                    <div className="mt-3 grid grid-cols-1 gap-3 md:grid-cols-2">
                      {d.simulation.fees !== undefined && (
                        <div>
                          <div className="mb-1 text-[11px] uppercase tracking-wider text-[var(--text-dim)]">Fees</div>
                          <pre className="max-h-48 overflow-auto rounded border border-[var(--border)] bg-[var(--bg-panel)] p-2 text-[12px]">
                            {JSON.stringify(d.simulation.fees, null, 2)}
                          </pre>
                        </div>
                      )}
                      {d.simulation.exposure !== undefined && (
                        <div>
                          <div className="mb-1 text-[11px] uppercase tracking-wider text-[var(--text-dim)]">Exposure</div>
                          <pre className="max-h-48 overflow-auto rounded border border-[var(--border)] bg-[var(--bg-panel)] p-2 text-[12px]">
                            {JSON.stringify(d.simulation.exposure, null, 2)}
                          </pre>
                        </div>
                      )}
                    </div>
                  )}
                </div>
              ) : (
                <p className="text-sm text-[var(--text-dim)]">No simulation was run for this opportunity.</p>
              )}
            </Section>
          </>
        )}
      </Await>
    </ConsoleShell>
  );
}

// LegsTable renders the evaluated legs as a table (audit ui F10): the
// backend's leg objects carry market/side/prices/fees per leg, and a
// JSON dump hides exactly the numbers a review needs to compare. Fields
// are read defensively (older rows predate some fields) and unknown
// shapes fall back to the raw JSON rather than an empty table.
function LegsTable({ legs }: { legs: unknown }) {
  const rows = Array.isArray(legs)
    ? legs.map((l) => l as Record<string, { String?: string; [k: string]: unknown } | string | number>)
    : [];
  const text = (v: unknown): string =>
    v === null || v === undefined
      ? "—"
      : typeof v === "object" && v !== null && "String" in (v as object)
        ? String((v as { String?: string }).String)
        : String(v);
  if (rows.length === 0) {
    return (
      <pre className="max-h-80 overflow-auto rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3 text-[12px]">
        {JSON.stringify(legs, null, 2)}
      </pre>
    );
  }
  return (
    <Table
      head={["#", "Market", "Side", "From→To", "Input", "Order qty", "VWAP", "Net out", "Fee", "Levels", "Impact bps", "Book v"]}
      empty="evaluated legs"
      rows={rows.map((l, i) => {
        const get = (k: string): unknown =>
          typeof l === "object" && l !== null ? l[k] : undefined;
        const market = get("Market");
        const marketText =
          typeof market === "object" && market !== null && "String" in (market as object)
            ? (market as { String?: string }).String ?? "—"
            : "—";
        return [
          i + 1,
          marketText,
          text(get("Side")),
          `${text(get("From"))} → ${text(get("To"))}`,
          text(get("InputConsumed")),
          text(get("OrderQty")),
          text(get("AvgPrice")),
          text(get("NetOut")),
          `${text(get("FeeAmount"))} ${text(get("FeeAsset"))}`,
          text(get("LevelsConsumed")),
          text(get("PriceImpactBps")),
          text(get("BookVersion")),
        ];
      })}
    />
  );
}
