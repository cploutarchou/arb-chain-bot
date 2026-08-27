"use client";

// FundingHistoryChart — replaces the ad-hoc FundingRateChart on /funding
// (design-system.md §4.6). Same hand-rolled-SVG, backend-values-only
// convention as ui.tsx's PnLSeriesChart/HistogramChart: Number()
// conversions below are ONLY for screen-space pixel geometry (x/y
// placement, nearest-point lookup) or a sign check for the fill colour —
// every rate rendered in an axis label or the hover tooltip is the
// backend's own decimal string, never a recomputed figure. One chart per
// venue, stacked vertically, since >5 series on one chart is explicitly
// out per design-system.md §5.3.

import { useMemo, useState } from "react";

export interface FundingSeries {
  venue: string;
  points: { at: string; rate: string }[];
}

function fmtTick(ms: number, spanMs: number): string {
  const d = new Date(ms);
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  if (spanMs > 72 * 3600 * 1000) {
    return `${String(d.getUTCDate()).padStart(2, "0")} ${hh}:${mm}`;
  }
  return `${hh}:${mm}`;
}

function OneVenueChart({
  venue,
  points,
  width,
}: {
  venue: string;
  points: { at: string; rate: string }[];
  width: number;
}) {
  const height = 120;
  const padL = 44;
  const padR = 8;
  const padT = 10;
  const padB = 18;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;

  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  const parsed = useMemo(
    () =>
      points.map((p) => ({
        ...p,
        ms: new Date(p.at).getTime(),
        val: Number(p.rate),
      })),
    [points],
  );

  if (parsed.length === 0) {
    return (
      <div className="mb-4">
        <div className="mb-1 text-[13px] font-semibold">{venue}</div>
        <p className="text-[13px] text-[var(--text-dim)]">
          No funding history for this selection.
        </p>
      </div>
    );
  }

  const minMs = Math.min(...parsed.map((p) => p.ms));
  const maxMs = Math.max(...parsed.map((p) => p.ms));
  const spanMs = maxMs - minMs || 1;

  let minIdx = 0;
  let maxIdx = 0;
  for (let i = 1; i < parsed.length; i++) {
    if (parsed[i]!.val < parsed[minIdx]!.val) minIdx = i;
    if (parsed[i]!.val > parsed[maxIdx]!.val) maxIdx = i;
  }
  const minVal = Math.min(0, parsed[minIdx]!.val);
  const maxVal = Math.max(0, parsed[maxIdx]!.val);
  const spanVal = maxVal - minVal || 1;

  const sx = (ms: number) => padL + ((ms - minMs) / spanMs) * plotW;
  const sy = (v: number) => padT + plotH - ((v - minVal) / spanVal) * plotH;
  const zeroY = sy(0);

  const linePath = parsed
    .map(
      (p, i) =>
        `${i === 0 ? "M" : "L"} ${sx(p.ms).toFixed(1)} ${sy(p.val).toFixed(1)}`,
    )
    .join(" ");

  // Positive/negative fill areas, split at zero crossings — sign is also
  // shown as text in the hover/table, this is reinforcement only (§4.6).
  const posArea = [
    `M ${sx(parsed[0]!.ms).toFixed(1)} ${zeroY.toFixed(1)}`,
    ...parsed.map(
      (p) => `L ${sx(p.ms).toFixed(1)} ${sy(Math.max(0, p.val)).toFixed(1)}`,
    ),
    `L ${sx(parsed[parsed.length - 1]!.ms).toFixed(1)} ${zeroY.toFixed(1)} Z`,
  ].join(" ");
  const negArea = [
    `M ${sx(parsed[0]!.ms).toFixed(1)} ${zeroY.toFixed(1)}`,
    ...parsed.map(
      (p) => `L ${sx(p.ms).toFixed(1)} ${sy(Math.min(0, p.val)).toFixed(1)}`,
    ),
    `L ${sx(parsed[parsed.length - 1]!.ms).toFixed(1)} ${zeroY.toFixed(1)} Z`,
  ].join(" ");

  const dotGapPx = parsed.length > 1 ? plotW / (parsed.length - 1) : plotW;
  const showDots = dotGapPx >= 8;

  const nearestFromClientX = (clientX: number, rect: DOMRect) => {
    const x = clientX - rect.left;
    const t = minMs + ((x - padL) / plotW) * spanMs;
    let best = 0;
    let bestDist = Infinity;
    for (let i = 0; i < parsed.length; i++) {
      const d = Math.abs(parsed[i]!.ms - t);
      if (d < bestDist) {
        bestDist = d;
        best = i;
      }
    }
    return best;
  };

  const tickEvery = Math.max(1, Math.ceil(parsed.length / 6));
  const hovered = hoverIdx !== null ? parsed[hoverIdx] : null;

  return (
    <div className="mb-4">
      <div className="mb-1 text-[13px] font-semibold">{venue}</div>
      <svg
        width={width}
        height={height}
        role="img"
        tabIndex={0}
        aria-label={`Funding rate history, ${venue}, n=${parsed.length} points, range ${parsed[minIdx]!.rate} to ${parsed[maxIdx]!.rate}`}
        onMouseMove={(e) =>
          setHoverIdx(
            nearestFromClientX(
              e.clientX,
              e.currentTarget.getBoundingClientRect(),
            ),
          )
        }
        onMouseLeave={() => setHoverIdx(null)}
        onKeyDown={(e) => {
          if (e.key === "ArrowRight") {
            e.preventDefault();
            setHoverIdx((i) => Math.min(parsed.length - 1, (i ?? -1) + 1));
          } else if (e.key === "ArrowLeft") {
            e.preventDefault();
            setHoverIdx((i) => Math.max(0, (i ?? parsed.length) - 1));
          }
        }}
        className="outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)]"
      >
        {/* y labels: min/0/max, right-aligned in the left gutter, verbatim
            backend strings (§4.6). */}
        <text
          x={padL - 6}
          y={sy(maxVal) + 3}
          textAnchor="end"
          fontSize={11}
          fill="var(--text-dim)"
        >
          {parsed[maxIdx]!.rate}
        </text>
        <text
          x={padL - 6}
          y={zeroY + 3}
          textAnchor="end"
          fontSize={11}
          fill="var(--text-dim)"
        >
          0
        </text>
        <text
          x={padL - 6}
          y={sy(minVal) + 3}
          textAnchor="end"
          fontSize={11}
          fill="var(--text-dim)"
        >
          {parsed[minIdx]!.rate}
        </text>

        <line
          x1={padL}
          y1={zeroY}
          x2={width - padR}
          y2={zeroY}
          stroke="var(--border-strong)"
          strokeWidth={1}
        />
        <path d={posArea} fill="var(--pos)" opacity={0.18} stroke="none" />
        <path d={negArea} fill="var(--neg)" opacity={0.18} stroke="none" />
        <path
          d={linePath}
          fill="none"
          stroke="var(--accent)"
          strokeWidth={1.5}
        />
        {showDots &&
          parsed.map((p, i) => (
            <circle
              key={i}
              cx={sx(p.ms)}
              cy={sy(p.val)}
              r={2}
              fill="var(--accent)"
              stroke="none"
            />
          ))}

        {parsed.map((p, i) =>
          i % tickEvery === 0 ? (
            <text
              key={i}
              x={sx(p.ms)}
              y={height - 4}
              textAnchor="middle"
              fontSize={11}
              fill="var(--text-dim)"
            >
              {fmtTick(p.ms, spanMs)}
            </text>
          ) : null,
        )}

        {hovered && (
          <>
            <line
              x1={sx(hovered.ms)}
              y1={padT}
              x2={sx(hovered.ms)}
              y2={height - padB}
              stroke="var(--border-strong)"
              strokeWidth={1}
            />
            <circle
              cx={sx(hovered.ms)}
              cy={sy(hovered.val)}
              r={3}
              fill="var(--accent)"
              stroke="none"
            />
          </>
        )}
      </svg>
      <div className="flex flex-wrap gap-4 text-[11px] text-[var(--text-dim)]">
        <span>n = {parsed.length}</span>
        <span>
          range {parsed[minIdx]!.rate} … {parsed[maxIdx]!.rate}
        </span>
        {hovered && (
          <span className="text-[var(--text)]">
            {hovered.at} · {hovered.rate}
          </span>
        )}
      </div>
    </div>
  );
}

export function FundingHistoryChart({
  series,
  width = 640,
}: {
  series: FundingSeries[];
  width?: number;
}) {
  if (series.length === 0 || series.every((s) => s.points.length === 0)) {
    return (
      <p className="text-[13px] text-[var(--text-dim)]">
        No funding history for this selection.
      </p>
    );
  }
  return (
    <div>
      {series.map((s) => (
        <OneVenueChart
          key={s.venue}
          venue={s.venue}
          points={s.points}
          width={width}
        />
      ))}
    </div>
  );
}
