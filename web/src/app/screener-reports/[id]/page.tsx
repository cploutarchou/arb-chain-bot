"use client";

// One screener paper report (T-078) — the §7 statistics table, the §8
// gate checklist (never a third state; an unevidenced item fails with a
// reason), the fixed model statement and notes, and the stored markdown
// rendered safely (ReportMarkdown — no raw HTML). Every figure is the
// backend's own decimal string, rendered verbatim.

import { useParams } from "next/navigation";
import { api, type ScreenerReportGateItem } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  HYPOTHETICAL_PERFORMANCE_FOOTER,
  ScreenerAwait,
} from "@/components/screener/ScreenerShared";
import { ReportMarkdown } from "@/components/screener/ReportMarkdown";
import {
  Badge,
  PageTitle,
  Section,
  Stat,
  Table,
  fmtTime,
  type Tone,
} from "@/components/ui";

function gateItemTone(status: string): Tone {
  return status === "pass" ? "ok" : "bad";
}

function DL({ rows }: { rows: [string, string][] }) {
  return (
    <dl className="grid grid-cols-2 gap-y-2 text-[13px] sm:grid-cols-4">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="text-[var(--text-dim)]">{label}</dt>
          <dd className="text-right sm:text-left">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

export default function ScreenerReportDetailPage() {
  const params = useParams<{ id: string }>();
  const id = decodeURIComponent(params.id);
  const detail = usePoll(() => api.screener.reports.get(id), 30000, [id]);

  return (
    <ConsoleShell>
      <PageTitle>Screener Report</PageTitle>
      <ScreenerAwait state={detail} what="screener report">
        {({ report: r }) => {
          const s = r.payload.stats;
          return (
            <>
              <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
                <Stat label="Strategy" value={r.payload.strategy} />
                <Stat
                  label="Rule"
                  value={
                    r.payload.rule_name || r.payload.rule_id || "aggregate"
                  }
                />
                <Stat
                  label="Window"
                  value={`${r.payload.window.label} · ${fmtTime(r.payload.window.start)} – ${fmtTime(r.payload.window.end)}`}
                />
                <Stat
                  label="Gate"
                  value={
                    <Badge
                      tone={
                        r.payload.gate_passed === r.payload.gate_total
                          ? "ok"
                          : r.payload.gate_passed > 0
                            ? "warn"
                            : "dim"
                      }
                    >
                      {r.payload.gate_passed}/{r.payload.gate_total} pass
                    </Badge>
                  }
                />
              </div>

              <Section title="Statistics">
                <div className="space-y-3">
                  <DL
                    rows={[
                      ["n", String(s.n)],
                      ["Wins", String(s.wins)],
                      ["Matched pairs", String(s.matched_pairs)],
                      ["Realised net PnL (quote)", s.realised_net_pnl_quote ?? s.net_pnl_quote ?? "—"],
                      ["Open positions", `${s.open_positions ?? 0} (unmarked ${s.unmarked_open_positions ?? 0})`],
                      ["Unrealised mark (quote)", `${s.unrealised_mark_quote ?? "—"} (mark age max ${s.open_mark_age_ms_max ?? "—"} ms)`],
                      ["Funding accrued (open)", s.funding_accrued_open ?? "—"],
                      ["Fees (quote)", s.fees_quote],
                      ["Funding (quote)", s.funding_quote],
                      ["PnL after rebalance", s.pnl_after_rebalance],
                      ["Matched-pair net", s.matched_pair_net],
                      ["Unwind cost", s.unwind_cost_quote],
                      [
                        "Conservative net (§8 item 3)",
                        s.conservative_net_quote,
                      ],
                      ["Net bps mean", s.net_bps_mean ?? "not computed"],
                      ["Net bps median", s.net_bps_median ?? "not computed"],
                      ["Hit rate", s.hit_rate ?? "not computed"],
                      [
                        "Hit rate 95% CI",
                        s.hit_rate_wilson95_low && s.hit_rate_wilson95_high
                          ? `[${s.hit_rate_wilson95_low}, ${s.hit_rate_wilson95_high}]`
                          : "not computed",
                      ],
                      [
                        "Lifetime mean / median (s)",
                        s.lifetime_s_mean && s.lifetime_s_median
                          ? `${s.lifetime_s_mean} / ${s.lifetime_s_median} (n=${s.lifetime_n})`
                          : "not computed",
                      ],
                      ["Max drawdown (quote)", s.max_drawdown_quote],
                      [
                        "Max drawdown (fraction)",
                        s.max_drawdown_frac ?? "not computed",
                      ],
                      ["Allocated capital (quote)", s.allocated_capital_quote],
                      [
                        "Realised slip mean / p95 (bps)",
                        s.realised_slip_bps_mean && s.realised_slip_bps_p95
                          ? `${s.realised_slip_bps_mean} / ${s.realised_slip_bps_p95} (n=${s.realised_slip_n})`
                          : "not computed",
                      ],
                      ["Slip allowance (bps)", s.slip_allowance_bps],
                      ["Concentration", s.concentration ?? "not computed"],
                      ["Largest loss (quote)", s.largest_loss_quote],
                      [
                        "Median win (quote)",
                        s.median_win_quote ?? "not computed",
                      ],
                      ["Days / weekend days", `${s.days} / ${s.weekend_days}`],
                      ["Funding rows", String(s.funding_rows)],
                    ]}
                  />
                  {s.wilcoxon && (
                    <p className="text-[12px] text-[var(--text-dim)]">
                      Wilcoxon (n={s.wilcoxon.n}): W+ {s.wilcoxon.w_plus}, z²{" "}
                      {s.wilcoxon.z_squared} —{" "}
                      {s.wilcoxon.rejects_h0
                        ? "rejects H₀"
                        : "does not reject H₀"}
                      .
                    </p>
                  )}
                  {s.bootstrap && (
                    <p className="text-[12px] text-[var(--text-dim)]">
                      Bootstrap ({s.bootstrap.resamples} resamples,{" "}
                      {s.bootstrap.days} days): mean {s.bootstrap.mean_net_bps}{" "}
                      bps, 95% CI [{s.bootstrap.ci95_low},{" "}
                      {s.bootstrap.ci95_high}].
                    </p>
                  )}
                  {s.skipped && Object.keys(s.skipped).length > 0 && (
                    <p className="text-[12px] text-[var(--text-dim)]">
                      Skipped:{" "}
                      {Object.entries(s.skipped)
                        .map(([reason, n]) => `${reason} (${n})`)
                        .join(", ")}
                    </p>
                  )}
                </div>
              </Section>

              <Section title="Gate checklist">
                <Table
                  head={["#", "Item", "Status", "Reason"]}
                  align={["num", "text", "text", "text"]}
                  empty="gate items"
                  rows={(r.payload.gate ?? []).map(
                    (g: ScreenerReportGateItem) => [
                      g.item,
                      g.name,
                      <Badge key="s" tone={gateItemTone(g.status)}>
                        {g.status}
                      </Badge>,
                      g.reason,
                    ],
                  )}
                />
              </Section>

              <Section title="Model & notes">
                <p className="mb-2 text-[13px]">{r.payload.model}</p>
                {r.payload.notes && r.payload.notes.length > 0 && (
                  <ul className="list-disc space-y-0.5 pl-5 text-[13px] text-[var(--text-dim)]">
                    {r.payload.notes.map((n, i) => (
                      <li key={i}>{n}</li>
                    ))}
                  </ul>
                )}
                <p className="mt-2 text-[12px] text-[var(--text-dim)]">
                  Data age:{" "}
                  {r.payload.data_age_ms !== null
                    ? `${r.payload.data_age_ms} ms`
                    : "no rows in this window"}{" "}
                  · Generated {fmtTime(r.payload.generated_at)}
                  {r.payload.files.markdown && (
                    <> · Filed at {r.payload.files.markdown}</>
                  )}
                </p>
              </Section>

              <Section title="Stored report (markdown)">
                <div className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3">
                  <ReportMarkdown markdown={r.md ?? ""} />
                </div>
              </Section>

              <p className="mt-3 max-w-3xl text-[12px] text-[var(--text-dim)]">
                {HYPOTHETICAL_PERFORMANCE_FOOTER}
              </p>
            </>
          );
        }}
      </ScreenerAwait>
    </ConsoleShell>
  );
}
