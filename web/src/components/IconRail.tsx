"use client";

// IconRail + NavGroupHeader — the shell's collapsible icon rail (design-
// system.md §4.1, UX §2.1). IconRail is the 44px column of per-group
// glyph buttons; NavGroupHeader is the label-column group header (title +
// chevron) it sits beside. Both are presentation-only: ConsoleShell still
// owns which groups are collapsed (persisted in localStorage) and never
// hides a group holding the active page.
//
// Rail buttons here EXPAND their group rather than collapsing everyone
// else (no forced accordion) — every Scanner Suite link must stay
// reachable/visible from a fresh session with default-expanded groups,
// which is what e2e/console.spec.ts's "nav group renders and every
// Scanner Suite page loads" asserts on a clean localStorage.

import { ChevronIcon, GroupIcon } from "@/components/icons";

export function IconRail({
  groups,
  activeGroupTitle,
  onSelect,
}: {
  groups: string[];
  activeGroupTitle?: string;
  onSelect: (title: string) => void;
}) {
  return (
    <div className="flex w-11 shrink-0 flex-col items-center gap-1 border-r border-[var(--border)] py-1">
      {groups.map((title) => {
        const active = title === activeGroupTitle;
        return (
          <button
            key={title}
            type="button"
            onClick={() => onSelect(title)}
            title={title}
            aria-label={title}
            className={`relative flex h-9 w-9 items-center justify-center rounded ${
              active
                ? "bg-[var(--bg-raised)] text-[var(--accent)]"
                : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
            }`}
          >
            {active && (
              <span
                aria-hidden
                className="absolute left-0 top-1 bottom-1 w-0.5 rounded-full bg-[var(--accent)]"
              />
            )}
            <GroupIcon group={title} />
          </button>
        );
      })}
    </div>
  );
}

export function NavGroupHeader({
  title,
  collapsed,
  onToggle,
  controlsId,
}: {
  title: string;
  collapsed: boolean;
  onToggle: () => void;
  controlsId: string;
}) {
  return (
    <button
      type="button"
      id={`${controlsId}-header`}
      onClick={onToggle}
      aria-expanded={!collapsed}
      aria-controls={controlsId}
      className="mb-1 flex h-6 w-full items-center justify-between rounded px-2 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-dim)] hover:text-[var(--text)]"
    >
      <span>{title}</span>
      <ChevronIcon
        className={`shrink-0 transition-transform duration-150 ${collapsed ? "-rotate-90" : ""}`}
      />
    </button>
  );
}
