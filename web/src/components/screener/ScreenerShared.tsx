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

// signTone picks a colour from the SIGN of a backend decimal string —
// string comparison against "-", never a parse-and-recompute of the
// value itself.
export function signTone(value: string | undefined): Tone {
  if (!value) return "dim";
  return value.trim().startsWith("-") ? "bad" : "ok";
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
            className={`rounded border px-2 py-0.5 text-[12px] ${
              on
                ? "border-[var(--accent)] text-[var(--accent)]"
                : "border-[var(--border)] text-[var(--text-dim)] hover:text-[var(--text)]"
            }`}
          >
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

// FundingRateChart: a small multi-series line chart in the same
// hand-rolled-SVG convention as ui.tsx's PnLSeriesChart — Number()
// conversions below are ONLY for screen-space x/y pixel geometry, never
// a recomputed funding/carry figure; every rate rendered in the legend
// or a tooltip would be the backend's own decimal string.
export function FundingRateChart({
  series,
  width = 640,
  height = 220,
}: {
  series: { venue: string; points: { at: string; rate: string }[] }[];
  width?: number;
  height?: number;
}) {
  const flat = series.flatMap((s) =>
    s.points.map((p) => ({
      venue: s.venue,
      at: new Date(p.at).getTime(),
      rate: Number(p.rate),
    })),
  );
  if (flat.length === 0) {
    return (
      <p className="text-[13px] text-[var(--text-dim)]">
        No funding history for this selection.
      </p>
    );
  }
  const minX = Math.min(...flat.map((p) => p.at));
  const maxX = Math.max(...flat.map((p) => p.at));
  const minY = Math.min(0, ...flat.map((p) => p.rate));
  const maxY = Math.max(0, ...flat.map((p) => p.rate));
  const spanX = maxX - minX || 1;
  const spanY = maxY - minY || 1;
  const padL = 8;
  const padR = 8;
  const padT = 10;
  const padB = 8;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;
  const sx = (t: number) => padL + ((t - minX) / spanX) * plotW;
  const sy = (v: number) => padT + plotH - ((v - minY) / spanY) * plotH;
  const zeroY = sy(0);
  const colors = [
    "var(--accent)",
    "var(--ok)",
    "var(--warn)",
    "var(--high)",
    "var(--critical)",
    "var(--text-dim)",
  ];
  return (
    <div>
      <svg
        width={width}
        height={height}
        role="img"
        aria-label="Funding rate history by venue"
      >
        <line
          x1={padL}
          y1={zeroY}
          x2={width - padR}
          y2={zeroY}
          stroke="var(--border)"
          strokeWidth={1}
        />
        {series.map((s, i) => {
          if (s.points.length === 0) return null;
          const d = s.points
            .map(
              (p, j) =>
                `${j === 0 ? "M" : "L"} ${sx(new Date(p.at).getTime()).toFixed(1)} ${sy(Number(p.rate)).toFixed(1)}`,
            )
            .join(" ");
          return (
            <path
              key={s.venue}
              d={d}
              fill="none"
              stroke={colors[i % colors.length]}
              strokeWidth={1.5}
            />
          );
        })}
      </svg>
      <div className="mt-1 flex flex-wrap gap-3 text-[11px] text-[var(--text-dim)]">
        {series.map((s, i) => (
          <span key={s.venue} className="flex items-center gap-1">
            <span
              aria-hidden
              style={{ background: colors[i % colors.length] }}
              className="inline-block h-2 w-2 rounded-full"
            />
            {s.venue} (n={s.points.length})
          </span>
        ))}
      </div>
    </div>
  );
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
