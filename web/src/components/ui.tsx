"use client";

// Small shared primitives for the console pages: dark-first, dense,
// honest empty/error states. No charts library yet — tables and stats
// carry the information; charts land with the analytics pass.

import { useEffect, useRef, type ReactNode } from "react";
import type { PollState } from "@/lib/usePoll";

export type Tone = "ok" | "warn" | "high" | "bad" | "dim";

export function PageTitle({ children }: { children: ReactNode }) {
  return <h1 className="mb-4 text-lg font-semibold">{children}</h1>;
}

export function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="mb-6">
      <h2 className="mb-2 text-[13px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
        {title}
      </h2>
      {children}
    </section>
  );
}

export function Stat({ label, value, tone }: { label: string; value: ReactNode; tone?: Tone }) {
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
      <div className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">{label}</div>
      <div className={`mt-1 truncate text-sm font-medium ${color}`}>{value}</div>
    </div>
  );
}

export function ErrorBox({ message, status }: { message: string; status?: number }) {
  return (
    <div className="rounded border border-[var(--critical)] bg-[var(--bg-panel)] p-4 text-sm">
      <span className="font-medium text-[var(--critical)]">
        {status === 401 ? "Session required:" : status === 403 ? "Forbidden:" : status === 404 ? "Unavailable:" : "Error:"}
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

// Await renders the three poll states uniformly.
export function Await<T>({ state, what, children }: { state: PollState<T>; what: string; children: (data: T) => ReactNode }) {
  if (state.kind === "loading") return <Loading what={what} />;
  if (state.kind === "error") return <ErrorBox message={state.message} status={state.status} />;
  return <>{children(state.data)}</>;
}

export function Table({ head, rows, empty }: { head: string[]; rows: ReactNode[][]; empty: string }) {
  if (rows.length === 0) return <Empty what={empty} />;
  return (
    <div className="overflow-x-auto rounded border border-[var(--border)]">
      <table className="w-full border-collapse text-[13px]">
        <thead>
          <tr className="bg-[var(--bg-panel)] text-left">
            {head.map((h) => (
              <th key={h} className="whitespace-nowrap px-3 py-2 font-medium text-[var(--text-dim)]">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((cells, i) => (
            <tr key={i} className="border-t border-[var(--border)] hover:bg-[var(--bg-panel)]">
              {cells.map((c, j) => (
                <td key={j} className="whitespace-nowrap px-3 py-1.5">
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

export function Badge({ tone, children }: { tone: Tone; children: ReactNode }) {
  const cls =
    tone === "ok"
      ? "border-[var(--ok)] text-[var(--ok)]"
      : tone === "warn"
        ? "border-[var(--warn)] text-[var(--warn)]"
        : tone === "high"
          ? "border-[var(--high)] text-[var(--high)]"
          : tone === "bad"
            ? "border-[var(--critical)] text-[var(--critical)]"
            : "border-[var(--border)] text-[var(--text-dim)]";
  return <span className={`inline-block rounded border px-1.5 py-0.5 text-[11px] ${cls}`}>{children}</span>;
}

export function Button({
  onClick,
  disabled,
  danger,
  children,
}: {
  onClick: () => void;
  disabled?: boolean;
  danger?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`rounded border px-2.5 py-1 text-[12px] font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${
        danger
          ? "border-[var(--critical)] text-[var(--critical)] hover:bg-[var(--critical)] hover:text-black"
          : "border-[var(--border)] text-[var(--text)] hover:bg-[var(--bg-raised)]"
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
  onConfirm,
  onCancel,
}: {
  title: string;
  body: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
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
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onClick={danger ? undefined : onCancel}
    >
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
        className="max-h-[85vh] w-full max-w-xl overflow-auto rounded border border-[var(--border)] bg-[var(--bg-panel)] p-4 outline-none"
      >
        <h2 className="mb-3 text-sm font-semibold">{title}</h2>
        <div className="mb-4 text-[13px] text-[var(--text-dim)]">{body}</div>
        <div className="flex justify-end gap-2">
          <Button onClick={onCancel}>{cancelLabel}</Button>
          <Button onClick={onConfirm} danger={danger}>
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
    return <p className="text-[13px] text-[var(--text-dim)]">No parameter changes.</p>;
  }
  return (
    <Table
      head={showEffect ? ["Parameter", beforeLabel, afterLabel, "Effect"] : ["Parameter", beforeLabel, afterLabel]}
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
  return Number.isNaN(d.getTime()) ? iso : d.toISOString().replace("T", " ").slice(0, 19) + "Z";
}
