"use client";

// OutcomeBadge is the one place a cycle outcome (CycleRow.outcome,
// SimulationResultView.outcome, TopOpportunity.outcome,
// ReportFailedCycle.outcome — all the same backend vocabulary) turns into
// a Badge. Renders the backend's code verbatim (never paraphrased) with
// the shared tone from lib/outcomes.ts, so Paper Trading, Triangle
// detail, Opportunity detail, Replay and Reports can never disagree with
// each other, or with the backend, about what a given outcome means.
import { Badge } from "@/components/ui";
import { outcomeInfo } from "@/lib/outcomes";

export function OutcomeBadge({
  code,
  showDescription,
}: {
  code: string | undefined | null;
  // showDescription: also render the one-line gloss as visible caption
  // text underneath — used on detail pages with room for it. Dense table
  // rows (Paper cycles, Triangle detail, Reports) omit it and rely on the
  // native title tooltip instead, the same space/detail trade-off this
  // codebase already makes for the Scanner Suite's STALE-age captions.
  showDescription?: boolean;
}) {
  const info = outcomeInfo(code);
  return (
    <span className={showDescription ? "inline-block" : undefined}>
      <Badge tone={info.tone}>
        <span title={info.description}>{info.code}</span>
      </Badge>
      {showDescription && (
        <span className="mt-1 block max-w-xs text-[11px] text-[var(--text-dim)]">
          {info.description}
        </span>
      )}
    </span>
  );
}
