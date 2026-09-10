"use client";

// Small shared primitives for the console pages: dark-first, dense,
// honest empty/error states. No charts library yet — tables and stats
// carry the information; charts land with the analytics pass.

import { useEffect, useRef, useState, type ReactNode } from "react";
import type { PollState } from "@/lib/usePoll";
import type { Distribution } from "@/lib/api/client";

export type Tone = "ok" | "warn" | "high" | "bad" | "dim";

export function PageTitle({ children }: { children: ReactNode }) {
  return <h1 className="mb-4 text-lg font-semibold">{children}</h1>;
}

export function Section({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="mb-6">
      <h2 className="mb-2 text-[13px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
        {title}
      </h2>
      {children}
    </section>
  );
}

export function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: ReactNode;
  tone?: Tone;
}) {
  const color =
    tone === "ok"
      ? "text-[var(--ok)]"
      : tone === "warn"
        ? "text-[var(--warn)]"
        : tone === "high"
          ? "text-[var(--high)]"
          : tone === "bad"
            ? "text-[var(--critical)]"
            : "text-[var(--text)]";
  return (
    <div className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3">
      <div className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">
        {label}
      </div>
      <div className={`mt-1 truncate text-sm font-medium ${color}`}>
        {value}
      </div>
    </div>
  );
}

// ABSENCE_CODES maps a backend "component not wired in this profile" error
// code to operator-language copy that names the doc, never an env var
// (audit §4.2: "an empty state ... never names an environment variable").
// Every needStore/needEngine/needReplays 404 in internal/api/*.go routes
// through this, so every new BL-17..BL-32 page gets the honest empty
// state for free instead of a bespoke branch per page.
const ABSENCE_CODES: Record<string, string> = {
  storage_absent: "Persistence isn't configured for this deployment yet.",
  engine_absent: "No trading engine is running in this deployment profile.",
  replays_absent:
    "The replay runner isn't available in this deployment (it needs persistence).",
  reporting_absent: "Reporting isn't running in this deployment profile.",
  portfolio_absent: "The paper portfolio hasn't initialized yet.",
  pnl_absent: "The paper portfolio hasn't initialized yet.",
  realtime_absent: "The realtime hub isn't running in this deployment.",
};

export function Unavailable({
  code,
  message,
}: {
  code?: string;
  message?: string;
}) {
  const copy =
    (code && ABSENCE_CODES[code]) ||
    message ||
    "Not available in this deployment.";
  return (
    <div className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-4 text-sm text-[var(--text-dim)]">
      {copy} See{" "}
      <code className="rounded bg-[var(--bg-raised)] px-1 py-0.5 text-[12px]">
        docs/deployment.md
      </code>{" "}
      if this deployment should have it configured.
    </div>
  );
}

// StaleVersionNotice is the shared "lost update" banner for the four
// optimistic-concurrency writes (T-058: config apply/rollback, platform
// settings apply/rollback). A 409 stale_version means another actor
// (another tab, Telegram, AI approval) applied a version while this
// operator was editing — the draft is never silently reconciled or
// re-sent; the only way forward is Reload, which the caller wires to
// discard the draft and refetch the current version.
export function StaleVersionNotice({
  currentVersion,
  onReload,
}: {
  currentVersion: number | null;
  onReload: () => void;
}) {
  return (
    <div className="mb-3 flex flex-wrap items-center gap-3 rounded border border-[var(--warn)] bg-[var(--bg-panel)] px-3 py-2 text-[13px] text-[var(--warn)]">
      <span>
        {currentVersion !== null
          ? `Someone applied version ${currentVersion} while you were editing — reload to continue.`
          : "Someone applied a newer version while you were editing — reload to continue."}
      </span>
      <Button onClick={onReload}>Reload</Button>
    </div>
  );
}

export function ErrorBox({
  message,
  status,
  code,
}: {
  message: string;
  status?: number;
  code?: string;
}) {
  if (code && ABSENCE_CODES[code])
    return <Unavailable code={code} message={message} />;
  return (
    <div className="rounded border border-[var(--critical)] bg-[var(--bg-panel)] p-4 text-sm">
      <span className="font-medium text-[var(--critical)]">
        {status === 401
          ? "Session required:"
          : status === 403
            ? "Forbidden:"
            : status === 404
              ? "Unavailable:"
              : "Error:"}
      </span>{" "}
      {message}
    </div>
  );
}

export function Loading({ what }: { what: string }) {
  return <p className="text-sm text-[var(--text-dim)]">Loading {what}…</p>;
}

export function Empty({ what }: { what: string }) {
  return <p className="text-sm text-[var(--text-dim)]">No {what}.</p>;
}

// DataAge renders "Updated 4s ago" for a poll's last success, going to
// a warn tone once the data is older than staleAfterMs (audit F9 — a
// polled money table must say when its numbers are from). Re-renders on
// a one-second tick so the age is live without the page polling faster.
export function DataAge({ lastOkAt, staleAfterMs = 15000 }: { lastOkAt?: number; staleAfterMs?: number }) {
  "use client";
  const [, setTick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);
  if (!lastOkAt) return null;
  const ageS = Math.max(0, Math.round((Date.now() - lastOkAt) / 1000));
  const stale = ageS * 1000 > staleAfterMs;
  return (
    <p className={`mt-1 text-[11px] ${stale ? "text-[var(--warn)]" : "text-[var(--text-dim)]"}`}>
      {stale ? "STALE — " : ""}Updated {ageS}s ago
    </p>
  );
}

// Await renders the three poll states uniformly. A transient error that
// still holds the last good payload renders that payload under a warn
// banner instead of blanking the table (audit F9); an error with nothing
// held (the first load never succeeded) is a plain error.
export function Await<T>({
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
    if (state.data === undefined)
      return (
        <ErrorBox
          message={state.message}
          status={state.status}
          code={state.code}
        />
      );
    return (
      <div>
        <p className="mb-2 rounded border border-[var(--warn)] px-3 py-2 text-sm text-[var(--warn)]" role="alert">
          Refresh failed ({state.message}) — showing the last good data.
        </p>
        {children(state.data)}
        <DataAge lastOkAt={state.lastOkAt} />
      </div>
    );
  }
  return (
    <div>
      {children(state.data)}
      <DataAge lastOkAt={state.lastOkAt} />
    </div>
  );
}

// ColumnAlign: per-column alignment for Table/VirtualTable (design-system.md
// §1.4 item 5 / §2.2 — numeric columns, and their header, right-aligned).
// Optional and parallel to `head`/each row's cell array rather than a
// change to `head`'s shape, so every existing string[] caller keeps
// compiling unchanged.
export type ColumnAlign = "num" | "text";

function alignClass(align?: ColumnAlign): string {
  return align === "num" ? "text-right" : "text-left";
}

export function Table({
  head,
  rows,
  empty,
  sticky,
  maxHeight,
  align,
}: {
  head: string[];
  rows: ReactNode[][];
  empty: string;
  // sticky: keep the header pinned while the table body scrolls past one
  // screen (§4.5 — Opportunity history, Audit log, Orders/Fills). Only
  // takes effect together with maxHeight, which bounds the scroll region.
  sticky?: boolean;
  maxHeight?: number;
  // align: one entry per column, defaulting to "text" (left) when absent
  // or shorter than `head` — never required, so existing callers are
  // unaffected until they opt a numeric column in.
  align?: ColumnAlign[];
}) {
  if (rows.length === 0) return <Empty what={empty} />;
  return (
    <div
      className="overflow-x-auto rounded border border-[var(--border)]"
      style={maxHeight ? { maxHeight, overflowY: "auto" } : undefined}
    >
      <table className="w-full border-collapse text-[13px] [font-variant-numeric:tabular-nums_slashed-zero]">
        <thead className={sticky ? "sticky top-0 z-10" : undefined}>
          <tr className="bg-[var(--bg-panel)] text-left">
            {head.map((h, i) => (
              <th
                key={h}
                className={`whitespace-nowrap px-3 py-2 font-medium text-[var(--text-dim)] ${alignClass(align?.[i])}`}
              >
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((cells, i) => (
            <tr
              key={i}
              className="group border-t border-[var(--border)] hover:bg-[var(--bg-panel)]"
            >
              {cells.map((c, j) => (
                <td
                  key={j}
                  className={`whitespace-nowrap px-3 py-1.5 ${alignClass(align?.[j])}`}
                >
                  {c}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ROW_HEIGHT/OVERSCAN back VirtualTable's windowing math; rows must be
// single-line (Table's own whitespace-nowrap convention) for the fixed
// height to hold.
const ROW_HEIGHT = 31;
const OVERSCAN = 12;

// VirtualTable renders every row directly below ~500 rows (matching
// Table's existing appearance exactly, including empty state) and
// switches to a windowed scroll region above that (§4.5/BL-22 — Orders,
// Fills, and any other table a long paper session can grow past 500
// rows). No virtualization dependency: a plain scroll container with
// spacer rows above/below the visible window.
export function VirtualTable({
  head,
  rows,
  empty,
  maxHeight = 480,
  threshold = 500,
  align,
}: {
  head: string[];
  rows: ReactNode[][];
  empty: string;
  maxHeight?: number;
  threshold?: number;
  // See Table's `align` — same per-column "num"/"text" option, threaded
  // through to both the direct-render and windowed-scroll branches below.
  align?: ColumnAlign[];
}) {
  const [scrollTop, setScrollTop] = useState(0);
  if (rows.length === 0) return <Empty what={empty} />;
  if (rows.length <= threshold) {
    return (
      <Table
        head={head}
        rows={rows}
        empty={empty}
        sticky
        maxHeight={maxHeight}
        align={align}
      />
    );
  }
  const visibleCount = Math.ceil(maxHeight / ROW_HEIGHT) + OVERSCAN * 2;
  const startIdx = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN);
  const endIdx = Math.min(rows.length, startIdx + visibleCount);
  const topSpacer = startIdx * ROW_HEIGHT;
  const bottomSpacer = (rows.length - endIdx) * ROW_HEIGHT;
  return (
    <div
      className="overflow-x-auto overflow-y-auto rounded border border-[var(--border)]"
      style={{ maxHeight }}
      onScroll={(e) => setScrollTop(e.currentTarget.scrollTop)}
      aria-label={`${rows.length} rows, virtualized`}
    >
      <table className="w-full border-collapse text-[13px] [font-variant-numeric:tabular-nums_slashed-zero]">
        <thead className="sticky top-0 z-10">
          <tr className="bg-[var(--bg-panel)] text-left">
            {head.map((h, i) => (
              <th
                key={h}
                className={`whitespace-nowrap px-3 py-2 font-medium text-[var(--text-dim)] ${alignClass(align?.[i])}`}
              >
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {topSpacer > 0 && (
            <tr aria-hidden style={{ height: topSpacer }}>
              <td colSpan={head.length} />
            </tr>
          )}
          {rows.slice(startIdx, endIdx).map((cells, i) => (
            <tr
              key={startIdx + i}
              className="group border-t border-[var(--border)] hover:bg-[var(--bg-panel)]"
              style={{ height: ROW_HEIGHT }}
            >
              {cells.map((c, j) => (
                <td
                  key={j}
                  className={`whitespace-nowrap px-3 py-1.5 ${alignClass(align?.[j])}`}
                >
                  {c}
                </td>
              ))}
            </tr>
          ))}
          {bottomSpacer > 0 && (
            <tr aria-hidden style={{ height: bottomSpacer }}>
              <td colSpan={head.length} />
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

export function Badge({ tone, children }: { tone: Tone; children: ReactNode }) {
  // Every tone outlines with --border-strong (design-system.md §1.4 item
  // 5: "Badge ... → border-[var(--border-strong)]") — a badge is a
  // control-identifying boundary, not a decorative rule, so it never
  // uses the weaker --border.
  const cls =
    tone === "ok"
      ? "border-[var(--border-strong)] text-[var(--ok)]"
      : tone === "warn"
        ? "border-[var(--border-strong)] text-[var(--warn)]"
        : tone === "high"
          ? "border-[var(--border-strong)] text-[var(--high)]"
          : tone === "bad"
            ? "border-[var(--border-strong)] text-[var(--critical)]"
            : "border-[var(--border-strong)] text-[var(--text-dim)]";
  return (
    <span
      className={`inline-block rounded border px-1.5 py-0.5 text-[11px] font-medium ${cls}`}
    >
      {children}
    </span>
  );
}

export function Button({
  onClick,
  disabled,
  danger,
  children,
  type = "button",
}: {
  onClick: () => void;
  disabled?: boolean;
  danger?: boolean;
  children: ReactNode;
  // Explicit "button" default: a <button> with no type attribute defaults
  // to "submit" per the HTML spec, so any Button placed inside a <form>
  // (Users & roles create form, Session password form) would otherwise
  // fire that form's onSubmit on click — including a plain "Cancel"
  // button, which must never trigger a mutation. Pass type="submit"
  // explicitly for the one button per form that should actually submit.
  type?: "button" | "submit";
}) {
  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled}
      className={`rounded border px-2.5 py-1 text-[12px] font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-[var(--opacity-disabled)] ${
        danger
          ? "border-[var(--critical)] text-[var(--critical)] hover:bg-[var(--critical)] hover:text-[var(--on-critical)]"
          : "border-[var(--border-strong)] text-[var(--text)] hover:bg-[var(--bg-raised)]"
      }`}
    >
      {children}
    </button>
  );
}

export function severityTone(sev: string): Tone {
  if (sev === "CRITICAL") return "bad";
  if (sev === "HIGH") return "high";
  if (sev === "WARNING") return "warn";
  return "dim";
}

// ConfirmDialog is the one shared confirmation primitive for dangerous or
// expensive actions (client-area.md: "explicit confirmation with
// before/after diffs"). Focus-trapped, Escape cancels; click-outside
// cancels only for non-destructive confirms (a stray click outside a
// destructive dialog must never be mistaken for a choice).
export function ConfirmDialog({
  title,
  body,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  danger,
  confirmDisabled,
  onConfirm,
  onCancel,
}: {
  title: string;
  body: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  // Gates the confirm button itself (e.g. type-to-confirm RESET) without
  // affecting Escape/Cancel, which must always work.
  confirmDisabled?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);

  // Focus once on mount only — re-running this on every parent re-render
  // (e.g. a page polling while the dialog is open) would steal focus back
  // from whatever the operator just tabbed to.
  useEffect(() => {
    ref.current?.focus();
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onCancel();
        return;
      }
      if (e.key !== "Tab" || !ref.current) return;
      const focusables = ref.current.querySelectorAll<HTMLElement>(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      );
      if (focusables.length === 0) return;
      const first = focusables[0]!;
      const last = focusables[focusables.length - 1]!;
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-[var(--overlay)] p-4"
      onClick={danger ? undefined : onCancel}
    >
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
        className="max-h-[85vh] w-full max-w-xl overflow-auto rounded border border-[var(--border-strong)] bg-[var(--bg-panel)] p-4 outline-none"
      >
        <h2 className="mb-3 text-sm font-semibold">{title}</h2>
        <div className="mb-4 text-[13px] text-[var(--text-dim)]">{body}</div>
        <div className="flex justify-end gap-2">
          <Button onClick={onCancel}>{cancelLabel}</Button>
          <Button
            onClick={onConfirm}
            danger={danger}
            disabled={confirmDisabled}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}

// DiffTable renders the before/after parameter table shape used by every
// confirmation dialog in §3.3 — actual values, never just changed keys.
export function DiffTable({
  rows,
  beforeLabel,
  afterLabel,
  showEffect,
}: {
  rows: { path: string; before: string; after: string; effect?: string }[];
  beforeLabel: string;
  afterLabel: string;
  showEffect?: boolean;
}) {
  if (rows.length === 0) {
    return (
      <p className="text-[13px] text-[var(--text-dim)]">
        No parameter changes.
      </p>
    );
  }
  return (
    <Table
      head={
        showEffect
          ? ["Parameter", beforeLabel, afterLabel, "Effect"]
          : ["Parameter", beforeLabel, afterLabel]
      }
      empty="changes"
      rows={rows.map((r) =>
        showEffect
          ? [r.path, r.before, r.after, r.effect ?? "immediate"]
          : [r.path, r.before, r.after],
      )}
    />
  );
}

export function fmtTime(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? iso
    : d.toISOString().replace("T", " ").slice(0, 19) + "Z";
}

// ---- Inline SVG charts (BL-19) --------------------------------------------
// No chart library — tables/stats carry the primary information per the
// codebase's existing convention; these are small hand-rolled panels.
// Number() conversions below are ONLY for pixel geometry (screen-space
// x/y); every axis label/tooltip renders the backend's own decimal
// string verbatim, and cumulative/drawdown/percentile/bucket values are
// read directly from the API response, never re-derived here.

export function PnLSeriesChart({
  points,
  width = 640,
  height = 200,
}: {
  points: { at: string; cumulative_pnl: string; drawdown: string }[];
  width?: number;
  height?: number;
}) {
  if (points.length === 0)
    return <p className="text-[13px] text-[var(--text-dim)]">No data.</p>;
  const cum = points.map((p) => Number(p.cumulative_pnl));
  const dd = points.map((p) => Number(p.drawdown));
  const minY = Math.min(0, ...cum, ...dd);
  const maxY = Math.max(0, ...cum);
  const spanY = maxY - minY || 1;
  const padL = 8;
  const padR = 8;
  const padT = 10;
  const padB = 8;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;
  const sx = (i: number) =>
    padL + (points.length <= 1 ? plotW / 2 : (i / (points.length - 1)) * plotW);
  const sy = (v: number) => padT + plotH - ((v - minY) / spanY) * plotH;
  const zeroY = sy(0);
  const cumPath = cum
    .map(
      (v, i) =>
        `${i === 0 ? "M" : "L"} ${sx(i).toFixed(1)} ${sy(v).toFixed(1)}`,
    )
    .join(" ");
  const ddArea = [
    `M ${sx(0).toFixed(1)} ${zeroY.toFixed(1)}`,
    ...dd.map((v, i) => `L ${sx(i).toFixed(1)} ${sy(v).toFixed(1)}`),
    `L ${sx(points.length - 1).toFixed(1)} ${zeroY.toFixed(1)} Z`,
  ].join(" ");
  // "Worst point" is a selection among already-backend-computed drawdown
  // values (which point to highlight), not a recomputation of the
  // peak-to-current drawdown formula itself.
  let worstIdx = 0;
  for (let i = 1; i < dd.length; i++) if (dd[i]! < dd[worstIdx]!) worstIdx = i;
  const first = points[0]!;
  const last = points[points.length - 1]!;
  const worst = points[worstIdx]!;
  return (
    <div>
      <svg
        width={width}
        height={height}
        role="img"
        aria-label="Cumulative P&L and drawdown over the selected window"
      >
        <line
          x1={padL}
          y1={zeroY}
          x2={width - padR}
          y2={zeroY}
          stroke="var(--border)"
          strokeWidth={1}
        />
        <path d={ddArea} fill="var(--critical)" opacity={0.18} stroke="none" />
        <path
          d={cumPath}
          fill="none"
          stroke="var(--accent)"
          strokeWidth={1.5}
        />
      </svg>
      <div className="mt-1 flex flex-wrap gap-4 text-[11px] text-[var(--text-dim)]">
        <span>n = {points.length}</span>
        <span>
          Start {fmtTime(first.at)}: {first.cumulative_pnl}
        </span>
        <span>
          End {fmtTime(last.at)}:{" "}
          <span className="text-[var(--text)]">{last.cumulative_pnl}</span>
        </span>
        <span>
          Max drawdown:{" "}
          <span className="text-[var(--critical)]">{worst.drawdown}</span> at{" "}
          {fmtTime(worst.at)}
        </span>
      </div>
    </div>
  );
}

// HistogramChart renders one Distribution's fixed-width buckets as bars;
// bar heights are proportional to the backend's own bucket counts —
// nothing here buckets, sorts, or computes a percentile.
export function HistogramChart({
  dist,
  width = 420,
  height = 120,
}: {
  dist: Distribution;
  width?: number;
  height?: number;
}) {
  if (dist.n === 0) {
    return (
      <p className="text-[13px] text-[var(--text-dim)]">
        No samples in this window (n = 0).
      </p>
    );
  }
  const buckets = dist.buckets ?? [];
  const maxCount = Math.max(1, ...buckets.map((b) => b.count));
  const padB = 6;
  const barW = buckets.length ? width / buckets.length : 0;
  return (
    <div>
      <svg
        width={width}
        height={height}
        role="img"
        aria-label="Distribution histogram"
      >
        {buckets.map((b, i) => {
          const h = (b.count / maxCount) * (height - padB - 4);
          return (
            <rect
              key={i}
              x={i * barW + 1}
              y={height - padB - h}
              width={Math.max(1, barW - 2)}
              height={h}
              fill="var(--accent)"
              opacity={0.75}
            >
              <title>{`${b.from} – ${b.to}: ${b.count}`}</title>
            </rect>
          );
        })}
        <line
          x1={0}
          y1={height - padB}
          x2={width}
          y2={height - padB}
          stroke="var(--border)"
          strokeWidth={1}
        />
      </svg>
      <div className="mt-1 flex flex-wrap gap-3 text-[11px] text-[var(--text-dim)]">
        <span>n = {dist.n}</span>
        {dist.min !== undefined && <span>min {dist.min}</span>}
        {dist.p50 !== undefined && <span>p50 {dist.p50}</span>}
        {dist.p95 !== undefined && <span>p95 {dist.p95}</span>}
        {dist.p99 !== undefined && <span>p99 {dist.p99}</span>}
        {dist.max !== undefined && <span>max {dist.max}</span>}
        {dist.avg !== undefined && <span>avg {dist.avg}</span>}
      </div>
    </div>
  );
}
