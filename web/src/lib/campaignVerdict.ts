// Shared campaign-verdict rendering logic (Campaigns Runs table + Overview
// "recent campaign verdicts"). Prefers the backend-supplied per-flag
// severity (`CampaignRun.verdicts`) once it ships; falls back to the
// existing frontend phrase list against `flags` until then. Text is
// always rendered verbatim — never summarized, truncated, or hidden
// behind a tooltip.

import type { CampaignRun } from "@/lib/api/client";
import { severityTone, type Tone } from "@/components/ui";

// Frontend phrase fallback — kept only until the backend emits a real
// severity alongside each flag (`verdicts`). Do not extend this list;
// extend the backend's severities instead.
const BAD_PHRASES = ["PROFITABLE ONLY UNDER PERFECT CONDITIONS", "UNPROFITABLE", "NO CYCLES"];

const KNOWN_TONES = new Set(["ok", "warn", "high", "bad", "dim"]);

// normalizeSeverity accepts either the backend's §80 verdict tones
// ("bad"/"warn"/"ok", already Tone-shaped) or the alert-style vocabulary
// (INFO/WARNING/HIGH/CRITICAL), so the console doesn't silently mis-rank
// a run if the backend's exact vocabulary changes.
function normalizeSeverity(sev: string): Tone {
  if (KNOWN_TONES.has(sev)) return sev as Tone;
  return severityTone(sev);
}

const TONE_RANK: Record<Tone, number> = { dim: -1, ok: 0, warn: 1, high: 2, bad: 3 };

export interface Verdict {
  text: string;
  tone: Tone;
}

// flagsTone summarizes a legacy `flags` map into a single tone. No
// verdict computed yet (`flags` undefined/empty) is NOT the same as "the
// verdict is good" — it must render dim, not ok.
export function flagsTone(flags: Record<string, string[]> | undefined): "ok" | "bad" | "dim" {
  if (!flags) return "dim";
  const all = Object.values(flags).flat();
  if (all.length === 0) return "dim";
  return all.some((f) => BAD_PHRASES.some((p) => f.includes(p))) ? "bad" : "ok";
}

// worstVerdict picks the single worst-severity flag headline for a run,
// verbatim, for the Runs table Verdict column and Overview's "recent
// campaign verdicts". Returns null when there is nothing to show yet
// (run still queued/running with no flags) — callers render "— pending —".
export function worstVerdict(run: CampaignRun): Verdict | null {
  if (run.verdicts) {
    let best: Verdict | null = null;
    for (const entries of Object.values(run.verdicts)) {
      for (const v of entries) {
        const tone = normalizeSeverity(v.severity || "ok");
        if (!best || TONE_RANK[tone] > TONE_RANK[best.tone]) {
          best = { text: v.text, tone };
        }
      }
    }
    return best;
  }
  if (run.flags) {
    const all = Object.values(run.flags).flat();
    if (all.length === 0) return null;
    const bad = all.find((f) => BAD_PHRASES.some((p) => f.includes(p)));
    if (bad) return { text: bad, tone: "bad" };
    return { text: all[0] ?? "—", tone: "ok" };
  }
  return null;
}

export { BAD_PHRASES };
