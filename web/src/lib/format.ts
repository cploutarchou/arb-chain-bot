// Shared display formatters for values several pages render the same
// way (audit ui F18/F9). Exposition only — nothing here recomputes a
// money or bps figure, and no value that reaches a decision, a ledger
// or a persisted row is ever formatted through this file.

// bookAgeStaleMs is the feed-stale design threshold (docs: no book
// updates for 30 s means qualification is suppressed — the same line
// the FeedStale alert uses). The word STALE, never colour alone, is
// the carrier of the state (design-system §1.7).
export const bookAgeStaleMs = 30_000;

// bookAgeText renders a book age with the STALE canon applied at the
// 30 s design threshold — raw ages made no threshold judgement at all
// (audit F9).
export function bookAgeText(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return "—";
  const text = fmtAgeMs(ms);
  return ms > bookAgeStaleMs ? `STALE ${text}` : text;
}

function fmtAgeMs(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${s % 60}s`;
}

// fmtBytes: byte counts for recordings/campaign artifacts, 1 KB = 1024.
export function fmtBytes(n: number): string {
  if (!Number.isFinite(n)) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

// fmtDurationSec renders whole seconds as "1h 2m 3s" (the campaigns and
// system pages' shared shape).
export function fmtDurationSec(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) return "—";
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  return `${h}h ${m}m ${s}s`;
}

// fmtDurationMs is fmtDurationSec over milliseconds.
export function fmtDurationMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  return fmtDurationSec(Math.floor(ms / 1000));
}
