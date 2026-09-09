// Cycle-outcome vocabulary (audit F4): mirrors internal/execution/
// executor.go's CycleOutcome constants exactly, plus ABORTED (a cycle
// interrupted by an engine shutdown or restart, added alongside the
// engine's own shutdown-drain handling). Every page that renders a
// settled cycle's outcome — Paper Trading, Triangle detail, Opportunity
// detail, Replay results, ops Reports — reads this one table instead of
// testing page-local strings, so the tone and the gloss can never drift
// from what the backend actually sends, and can never drift from each
// other between pages.
//
// Tone follows the backend's own risk model, not the input character
// count: ALL_FILLED is the only unambiguous good outcome. LEG1_PARTIAL
// and PARTIAL_CYCLE completed — smaller than planned, but nothing is
// stranded — so they warn rather than alarm. A mid-cycle failure, a
// TIMEOUT or an ABORTED shutdown can all leave capital deployed in a
// non-start asset with no further leg to complete it, which is the one
// condition this console must never let read as a benign amber warning
// (the audit's original complaint) — those are "danger" (Badge's "bad"
// tone). EXPIRED and REJECTED never deployed any capital at all, so they
// are informational, not alarming ("neutral" / Badge's "dim" tone).
import type { Tone } from "@/components/ui";

export interface OutcomeInfo {
  /** Rendered verbatim — the backend's own code, never paraphrased. */
  code: string;
  /** One-line gloss of what happened and what it means for exposure. */
  description: string;
  tone: Tone;
}

type OutcomeToneName = "ok" | "warn" | "danger" | "neutral";

const TONE: Record<OutcomeToneName, Tone> = {
  ok: "ok",
  warn: "warn",
  danger: "bad",
  neutral: "dim",
};

const DEFS: Record<string, { description: string; tone: OutcomeToneName }> = {
  ALL_FILLED: {
    description: "Every leg filled as planned — the cycle completed in full.",
    tone: "ok",
  },
  LEG1_PARTIAL: {
    description: "Leg 1 only partially filled — the cycle completed smaller than planned.",
    tone: "warn",
  },
  PARTIAL_CYCLE: {
    description: "The cycle completed with a smaller fill than planned.",
    tone: "warn",
  },
  LEG1_FILLED_LEG2_FAILED: {
    description: "Leg 1 filled, leg 2 failed — capital is stranded in the leg-1 asset.",
    tone: "danger",
  },
  LEG1_LEG2_FILLED_LEG3_FAILED: {
    description: "Legs 1 and 2 filled, leg 3 failed — capital is stranded short of the start asset.",
    tone: "danger",
  },
  TIMEOUT: {
    description: "The cycle did not settle before its deadline — capital may be stranded mid-cycle.",
    tone: "danger",
  },
  ABORTED: {
    description:
      "Interrupted by an engine shutdown or restart — capital deployed before the interruption is stranded exposure.",
    tone: "danger",
  },
  EXPIRED: {
    description: "The opportunity expired before execution started — nothing was deployed.",
    tone: "neutral",
  },
  REJECTED: {
    description: "Execution was rejected before any order was placed — nothing was deployed.",
    tone: "neutral",
  },
};

const UNKNOWN_DESCRIPTION = "Outcome code not recognised by this console build.";

// outcomeInfo never throws and never hides an outcome the backend sends
// but this table doesn't yet know about — it renders the code verbatim
// with a neutral tone and an honest "not recognised" gloss instead.
export function outcomeInfo(code: string | undefined | null): OutcomeInfo {
  const c = code ?? "—";
  const def = code ? DEFS[code] : undefined;
  return {
    code: c,
    description: def?.description ?? UNKNOWN_DESCRIPTION,
    tone: def ? TONE[def.tone] : "dim",
  };
}
