// Feed-state derivation for the status strip (audit F5): one honest
// word for "can the engine see the market right now", derived from the
// book states the health endpoint already reports — never re-fetched,
// never recomputed beyond counting. The states are the backend's own
// (HEALTHY / STALE / CORRUPTED / SYNCING / DISCONNECTED); anything
// other than HEALTHY is "not healthy", with the counts kept so the
// operator sees how deep the degradation goes.
import type { Tone } from "@/components/ui";

export interface FeedStateInfo {
  /** Short cell value: CONNECTED / DEGRADED / DISCONNECTED / RATE-LIMITED / NOT STARTED. */
  label: string;
  /** One-line counts: "2 of 6 books not healthy (STALE ×2)". */
  detail: string;
  tone: Tone;
}

export function feedState(
  books: { market: string; state: string; age_ms: number }[] | undefined,
  rateLimited: number | undefined,
): FeedStateInfo {
  if (!books || books.length === 0) {
    return {
      label: "NOT STARTED",
      detail: "no books yet — the feed has not delivered a snapshot",
      tone: "dim",
    };
  }
  const byState = new Map<string, number>();
  let healthy = 0;
  for (const b of books) {
    if (b.state === "HEALTHY") {
      healthy++;
      continue;
    }
    byState.set(b.state, (byState.get(b.state) ?? 0) + 1);
  }
  const notHealthy = books.length - healthy;
  const breakdown = [...byState.entries()]
    .map(([state, n]) => `${state} ×${n}`)
    .join(", ");
  if (rateLimited && rateLimited > 0) {
    return {
      label: "RATE-LIMITED",
      detail: `REST rate-limited ${rateLimited}× — data may stall`,
      tone: "warn",
    };
  }
  if (healthy === books.length) {
    return {
      label: "CONNECTED",
      detail: `all ${books.length} books healthy`,
      tone: "ok",
    };
  }
  if (healthy === 0) {
    return {
      label: "DISCONNECTED",
      detail: `0 of ${books.length} books healthy (${breakdown})`,
      tone: "bad",
    };
  }
  return {
    label: "DEGRADED",
    detail: `${notHealthy} of ${books.length} books not healthy (${breakdown})`,
    tone: "warn",
  };
}

// fmtDurationMs: display-only duration formatter for elapsed/duration
// cells (ms input; presentation, never part of a money calculation).
export function fmtDurationMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)} s`;
  const m = Math.floor(s / 60);
  const rem = Math.round(s - m * 60);
  if (m < 60) return `${m}m ${rem}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m - h * 60}m`;
}
