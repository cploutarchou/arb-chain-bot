"use client";

// FilterCard — the shared filter-card shape (design-system.md §4.7, UX
// §6.1), extracted from the Screener page and reused on Perpetuals. The
// card owns only chrome (border, padding, mobile collapse); each page
// supplies its own venue-chip groups / numeric filters / template row as
// children, since the filterable fields differ per page (§6.1 already
// documents that: two venue groups on Screener, one on Perpetuals/
// Funding).

import { useState, type ReactNode } from "react";
import { Badge } from "@/components/ui";

export function FilterCard({
  activeCount,
  children,
}: {
  activeCount: number;
  children: ReactNode;
}) {
  const [openMobile, setOpenMobile] = useState(false);
  return (
    <div className="mb-4 rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3">
      <button
        type="button"
        onClick={() => setOpenMobile((v) => !v)}
        aria-expanded={openMobile}
        className="mb-2 flex items-center gap-2 text-[13px] font-medium text-[var(--text)] md:hidden"
      >
        Filters ({activeCount} active)
        {activeCount > 0 && <Badge tone="dim">{activeCount}</Badge>}
      </button>
      <div className={`${openMobile ? "block" : "hidden"} space-y-3 md:block`}>
        {children}
      </div>
    </div>
  );
}

// FilterRow: a labelled group inside FilterCard (venue chips, quote
// select, etc) — just the label typography, layout stays with the caller.
export function FilterRow({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 text-[11px] uppercase tracking-wider text-[var(--text-dim)]">
        {label}
      </div>
      {children}
    </div>
  );
}

// NumericFilterField — §4.7 item 2: labelled input, right-aligned digits,
// unit suffix inside the field in --text-dim, border-strong, focus
// border --accent.
export function NumericFilterField({
  label,
  value,
  onChange,
  unit,
  width = "w-28",
  inputMode = "decimal",
  invalid,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  unit?: string;
  width?: string;
  inputMode?: "decimal" | "numeric";
  invalid?: string;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-[12px] text-[var(--text-dim)]">{label}</span>
      <span
        className={`flex h-[26px] items-center gap-1 rounded border px-2 focus-within:border-[var(--accent)] ${width} ${
          invalid ? "border-[var(--critical)]" : "border-[var(--border-strong)]"
        }`}
      >
        <input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          inputMode={inputMode}
          className="w-full min-w-0 bg-transparent text-right text-[13px] outline-none [font-variant-numeric:tabular-nums]"
        />
        {unit && (
          <span className="shrink-0 text-[11px] text-[var(--text-dim)]">
            {unit}
          </span>
        )}
      </span>
      {invalid && (
        <span className="text-[11px] text-[var(--critical)]">{invalid}</span>
      )}
    </label>
  );
}

// TextFilterField — the same chrome as NumericFilterField for a plain
// text input (quote select uses a native <select>, base allow/deny use
// this for comma-separated tokens).
export function TextFilterField({
  label,
  value,
  onChange,
  placeholder,
  width = "w-56",
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  width?: string;
}) {
  return (
    <label className={`flex flex-col gap-1 ${width}`}>
      <span className="text-[12px] text-[var(--text-dim)]">{label}</span>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="h-[26px] rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 text-[13px] outline-none focus:border-[var(--accent)]"
      />
    </label>
  );
}

export function SelectFilterField({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-[12px] text-[var(--text-dim)]">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="h-[26px] rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 text-[13px] outline-none"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
}
