"use client";

// FilterCard — the shared filter-card shape (design-system.md §4.7, UX
// §6.1), extracted from the Screener page and reused on Perpetuals. The
// card owns only chrome (border, padding, mobile collapse, the optional
// Advanced disclosure and applied-filter chip row); each page supplies
// its own venue-chip groups / numeric filters / template row as children,
// since the filterable fields differ per page (§6.1 already documents
// that: two venue groups on Screener, one on Perpetuals/Funding).
//
// `chips`/`onClearAll`/`advanced`/`advancedStorageKey` are all optional
// (T-087 §A2/§A3) so `perpetuals/page.tsx`'s existing
// `<FilterCard activeCount={activeFilterCount}>…</FilterCard>` — with no
// knowledge of these props — keeps compiling and rendering exactly as
// before.

import { useEffect, useState, type ReactNode } from "react";
import { Badge, Button } from "@/components/ui";
import { PlusMinusIcon } from "@/components/icons";

export interface FilterChip {
  key: string;
  // Visible chip text, e.g. "Buy: binance, okx" — the filter name and its
  // current value, so the chip row reads as a sentence, not a bare list
  // of unlabeled values.
  label: string;
  onRemove: () => void;
}

export function FilterCard({
  activeCount,
  chips,
  onClearAll,
  advanced,
  advancedStorageKey,
  advancedLabel = "Advanced filters",
  children,
}: {
  activeCount: number;
  // chips/onClearAll: the applied-filter summary row (T-087 §A3) — a
  // removable chip per active filter plus one "Clear filters" action.
  // Omitted entirely (as Perpetuals does today) when the caller passes
  // neither.
  chips?: FilterChip[];
  onClearAll?: () => void;
  // advanced: a second children slot disclosed behind a collapsed-by-
  // default toggle (T-087 §A2 — "Advanced disclosed"). Persisted under
  // advancedStorageKey so a deliberate "show me the advanced fields"
  // choice survives a reload, using the same localStorage convention as
  // scanner/page.tsx's pinned-triangle set (try/catch, read post-
  // hydration in an effect so SSR/CSR never mismatch).
  advanced?: ReactNode;
  advancedStorageKey?: string;
  advancedLabel?: string;
  children: ReactNode;
}) {
  const [openMobile, setOpenMobile] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);

  useEffect(() => {
    if (!advancedStorageKey) return;
    try {
      setAdvancedOpen(
        window.localStorage.getItem(advancedStorageKey) === "1",
      );
    } catch {
      // Private mode / quota — advanced just starts collapsed.
    }
  }, [advancedStorageKey]);

  const toggleAdvanced = () => {
    setAdvancedOpen((was) => {
      const next = !was;
      if (advancedStorageKey) {
        try {
          window.localStorage.setItem(advancedStorageKey, next ? "1" : "0");
        } catch {
          // ignore — the toggle still works for this session
        }
      }
      return next;
    });
  };

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

        {advanced && (
          <div className="border-t border-[var(--border)] pt-3">
            <button
              type="button"
              onClick={toggleAdvanced}
              aria-expanded={advancedOpen}
              className="flex items-center gap-1.5 text-[12px] font-medium text-[var(--accent)] hover:underline"
            >
              <PlusMinusIcon open={advancedOpen} />
              {advancedOpen
                ? `Hide ${advancedLabel.toLowerCase()}`
                : `Show ${advancedLabel.toLowerCase()}`}
            </button>
            {advancedOpen && (
              <div className="mt-3 space-y-3">{advanced}</div>
            )}
          </div>
        )}

        {chips && chips.length > 0 && (
          <div className="flex flex-wrap items-center gap-2 border-t border-[var(--border)] pt-3">
            <span className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">
              Active filters ({chips.length})
            </span>
            {chips.map((c) => (
              <button
                key={c.key}
                type="button"
                onClick={c.onRemove}
                aria-label={`Remove filter: ${c.label}`}
                className="rounded border border-[var(--border-strong)] px-2 py-0.5 text-[12px] text-[var(--text)] hover:bg-[var(--bg-raised)]"
              >
                {c.label} ✕
              </button>
            ))}
            {onClearAll && <Button onClick={onClearAll}>Clear filters</Button>}
          </div>
        )}
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
// border --accent. `hint` is optional helper copy under the field (T-087
// §C1 "field help") — a plain caption, never the carrier of validity on
// its own (that is `invalid`).
export function NumericFilterField({
  label,
  value,
  onChange,
  unit,
  width = "w-28",
  inputMode = "decimal",
  invalid,
  hint,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  unit?: string;
  width?: string;
  inputMode?: "decimal" | "numeric";
  invalid?: string;
  hint?: string;
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
          aria-invalid={invalid ? true : undefined}
          className="w-full min-w-0 bg-transparent text-right text-[13px] outline-none [font-variant-numeric:tabular-nums]"
        />
        {unit && (
          <span className="shrink-0 text-[11px] text-[var(--text-dim)]">
            {unit}
          </span>
        )}
      </span>
      {invalid ? (
        <span className="text-[11px] text-[var(--critical)]">{invalid}</span>
      ) : (
        hint && (
          <span className="text-[11px] text-[var(--text-dim)]">{hint}</span>
        )
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
  hint,
  invalid,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  width?: string;
  hint?: string;
  invalid?: string;
}) {
  return (
    <label className={`flex flex-col gap-1 ${width}`}>
      <span className="text-[12px] text-[var(--text-dim)]">{label}</span>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        aria-invalid={invalid ? true : undefined}
        className={`h-[26px] rounded border bg-[var(--bg)] px-2 text-[13px] outline-none focus:border-[var(--accent)] ${
          invalid ? "border-[var(--critical)]" : "border-[var(--border-strong)]"
        }`}
      />
      {invalid ? (
        <span className="text-[11px] text-[var(--critical)]">{invalid}</span>
      ) : (
        hint && (
          <span className="text-[11px] text-[var(--text-dim)]">{hint}</span>
        )
      )}
    </label>
  );
}

export function SelectFilterField({
  label,
  value,
  onChange,
  options,
  hint,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
  hint?: string;
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
      {hint && (
        <span className="text-[11px] text-[var(--text-dim)]">{hint}</span>
      )}
    </label>
  );
}
