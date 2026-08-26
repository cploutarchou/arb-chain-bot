"use client";

// Small shared primitives for the console pages: dark-first, dense,
// honest empty/error states. No charts library yet — tables and stats
// carry the information; charts land with the analytics pass.

import type { ReactNode } from "react";
import type { PollState } from "@/lib/usePoll";

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

export function Stat({ label, value, tone }: { label: string; value: ReactNode; tone?: "ok" | "warn" | "bad" }) {
  const color =
    tone === "ok"
      ? "text-[var(--ok)]"
      : tone === "warn"
        ? "text-[var(--warn)]"
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

export function Badge({ tone, children }: { tone: "ok" | "warn" | "bad" | "dim"; children: ReactNode }) {
  const cls =
    tone === "ok"
      ? "border-[var(--ok)] text-[var(--ok)]"
      : tone === "warn"
        ? "border-[var(--warn)] text-[var(--warn)]"
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

export function severityTone(sev: string): "ok" | "warn" | "bad" | "dim" {
  if (sev === "CRITICAL") return "bad";
  if (sev === "WARNING") return "warn";
  return "dim";
}

export function fmtTime(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toISOString().replace("T", " ").slice(0, 19) + "Z";
}
