"use client";

// Shared primitives for the Scanner Suite pages (screener, perpetuals,
// funding, calculator, scanner-alerts, auto-paper). Presentation only —
// every bps/price/liquidity/PnL number the backend returns is rendered
// verbatim; the only arithmetic here is wall-clock (data age, countdown)
// or a sign check for colour, never a spread/fee/PnL recomputation.

import { useEffect, useState, type ReactNode } from "react";
import {
  api,
  type ScreenerNetworkState,
  type ScreenerStatusView,
} from "@/lib/api/client";
import { usePoll, type PollState } from "@/lib/usePoll";
import { Badge, ErrorBox, Loading, type Tone } from "@/components/ui";
import { CheckIcon } from "@/components/icons";

// ScreenerAwait degrades a missing/unbuilt screener backend to one honest
// notice instead of the generic ErrorBox: a 404 (route not registered
// yet) or 503 (component not wired in this profile) both mean "the
// backend for this suite isn't in this build," which is a real, expected
// state while T-065..T-072 land — never a crash, never a stale table.
export function ScreenerAwait<T>({
  state,
  what,
  children,
}: {
  state: PollState<T>;
  what: string;
  children: (data: T) => ReactNode;
}) {
  if (state.kind === "loading") return <Loading what={what} />;
  if (state.kind === "error") {
    if (state.status === 404 || state.status === 503) {
      return (
        <div className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-4 text-sm text-[var(--text-dim)]">
          Screener backend not available in this build.
        </div>
      );
    }
    return (
      <ErrorBox
        message={state.message}
        status={state.status}
        code={state.code}
      />
    );
  }
  return <>{children(state.data)}</>;
}

// Countdown ticks its own 1s timer, isolated to whatever cell renders it
// — the enclosing table/page does not re-render every second (§ "never
// rerender the whole dashboard per tick").
export function Countdown({ target }: { target?: string }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  if (!target) return <span className="text-[var(--text-dim)]">—</span>;
  const ms = new Date(target).getTime() - now;
  if (!Number.isFinite(ms))
    return <span className="text-[var(--text-dim)]">—</span>;
  if (ms <= 0) return <span className="text-[var(--warn)]">due</span>;
  const s = Math.floor(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return (
    <span>
      {h > 0 ? `${h}h ` : ""}
      {m}m {sec}s
    </span>
  );
}

// fmtAge renders a millisecond age as a short human string — display
// only, not a data value.
export function fmtAge(ms?: number): string {
  if (ms === undefined || !Number.isFinite(ms)) return "—";
  if (ms < 1000) return `${ms}ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${s % 60}s`;
}

// isStaleAge implements §3's "rows older than 3× poll interval are
// greyed" rule — a threshold comparison, not a recomputed number.
export function isStaleAge(
  ms: number | undefined,
  pollIntervalS: number,
): boolean {
  if (ms === undefined) return false;
  return ms > pollIntervalS * 1000 * 3;
}

// ageTone implements the three-band data-age rule shared by every Scanner
// Suite table (design-system.md §1.7 / UX §3): <=1x poll = dim, 1-3x =
// warn, >3x = bad. Threshold comparisons only, never a recomputed number.
export function ageTone(ms: number | undefined, pollIntervalS: number): Tone {
  if (ms === undefined) return "dim";
  const poll = Math.max(1, pollIntervalS) * 1000;
  if (ms <= poll) return "dim";
  if (ms <= poll * 3) return "warn";
  return "bad";
}

// ageCellText renders the literal "STALE {age}" text required by §1.7 —
// the carrier of the stale state is the word, never colour alone.
export function ageCellText(
  ms: number | undefined,
  pollIntervalS: number,
): string {
  const text = fmtAge(ms);
  return ageTone(ms, pollIntervalS) === "bad" ? `STALE ${text}` : text;
}

// staleCellClass: per-cell opacity dimming for every cell in a stale row
// EXCEPT the age cell(s) itself (§1.7 — the STALE text stays full
// opacity, dimming is secondary reinforcement on the rest of the row).
// group-hover:opacity-100 suspends the dim on a hovered row (dimmed
// --text-dim over --bg-raised falls under AA — §1.7 "hover suspends the
// dim"); Table/VirtualTable's <tr> carries the `group` class this reads.
export function staleCellClass(stale: boolean): string {
  return stale ? "opacity-[var(--opacity-stale)] group-hover:opacity-100" : "";
}

// signTone picks a colour from the SIGN of a backend decimal string —
// string comparison against "-", never a parse-and-recompute of the
// value itself.
export function signTone(value: string | undefined): Tone {
  if (!value) return "dim";
  return value.trim().startsWith("-") ? "bad" : "ok";
}

// signedText prefixes an explicit "+" on a positive backend decimal
// string (design-system.md §1.8: "Sign is on the number ... never a bare
// figure that needs the colour to be read"). This only ever prepends a
// character already implied by the absence of "-" in the backend's own
// string — it is not a parse-and-recompute, the digits are untouched.
export function signedText(value: string | undefined): string {
  if (value === undefined || value === "") return "—";
  const t = value.trim();
  if (t === "—" || t.startsWith("-") || t.startsWith("+")) return value;
  if (/^0(\.0+)?$/.test(t)) return value;
  return `+${value}`;
}

const NETWORK_TONE: Record<ScreenerNetworkState, Tone> = {
  open: "ok",
  closed: "bad",
  unknown: "dim",
};

export function NetworkBadge({
  state,
  reason,
}: {
  state: ScreenerNetworkState;
  reason?: string;
}) {
  return (
    <span
      title={
        reason ??
        (state === "unknown" ? "unknown (venue requires API key)" : undefined)
      }
    >
      <Badge tone={NETWORK_TONE[state]}>{state}</Badge>
    </span>
  );
}

// VENUE_OPTIONS: the six target venues from the design (§0/§2); pages
// use this only to render filter chips — the backend's own
// GET /screener/status venues[] is the source of truth for what is
// actually enabled/online, this list is just the chip vocabulary before
// that response arrives.
export const VENUE_OPTIONS = [
  "binance",
  "okx",
  "bybit",
  "bitget",
  "gate",
  "mexc",
];

// VenueChips — design-system.md §4.7 chip spec: 24px tall, border-strong
// outline; "on" fills --accent with --on-accent ink and a leading check
// glyph, "off" stays unfilled with --text-dim ink; hover only paints
// --bg-raised on the off state (the on state is already the strongest
// visual, so hover leaves it unchanged).
export function VenueChips({
  selected,
  onToggle,
}: {
  selected: string[];
  onToggle: (venue: string) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {VENUE_OPTIONS.map((v) => {
        const on = selected.includes(v);
        return (
          <button
            key={v}
            type="button"
            onClick={() => onToggle(v)}
            aria-pressed={on}
            className={`flex h-6 items-center gap-1 rounded border border-[var(--border-strong)] px-2 text-[12px] font-medium ${
              on
                ? "bg-[var(--accent)] text-[var(--on-accent)]"
                : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
            }`}
          >
            {on && <CheckIcon />}
            {v}
          </button>
        );
      })}
    </div>
  );
}

// useScreenerStatus/pollMsFromStatus: every Scanner Suite page polls its
// data at the backend's own poll_interval_s (design §5: "auto-refreshing
// every poll"), defaulting to 3s until status has loaded — never a
// frontend-guessed interval once status is known.
export function useScreenerStatus(): PollState<ScreenerStatusView> {
  return usePoll(() => api.screener.status(), 5000);
}

export function pollMsFromStatus(
  status: PollState<ScreenerStatusView>,
): number {
  const s = status.kind === "ready" ? status.data.poll_interval_s : 0;
  return s > 0 ? s * 1000 : 3000;
}

export function pollIntervalSFromStatus(
  status: PollState<ScreenerStatusView>,
): number {
  const s = status.kind === "ready" ? status.data.poll_interval_s : 0;
  return s > 0 ? s : 3;
}

// parseCsv: shared comma-separated-list parser for base allow/deny,
// quotes, etc. — trims, upper-cases, drops empties. Presentation-only
// text handling, not a financial computation.
export function parseCsv(text: string): string[] {
  return text
    .split(",")
    .map((s) => s.trim().toUpperCase())
    .filter(Boolean);
}

// NO_TRANSFER_NOTE is the required footer disclaimer (design §3/§5):
// every screener/perp page that shows a spread or carry number carries
// it, worded identically everywhere.
export const NO_TRANSFER_NOTE =
  "No-transfer model, top-of-book liquidity, net of taker fees — nothing here is a guarantee.";
