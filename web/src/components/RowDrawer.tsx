"use client";

// RowDrawer — the right-hand row-detail panel (design-system.md §4.3, UX
// §4/§9). Non-modal (`role="complementary"`, no aria-modal, no focus
// trap): the underlying table stays interactive so an operator can keep
// scanning while comparing one row. Full-screen with a scrim on mobile
// (< md) since there is no room for a side panel there.
//
// Focus management: moves to the close button on open, returns to
// whatever triggered the drawer (`returnFocusRef`) on close; Escape
// closes. This mirrors ConfirmDialog's keyboard contract minus the modal
// trap.

import { useEffect, useRef, type ReactNode } from "react";
import { CloseIcon } from "@/components/icons";
import { Badge, type Tone } from "@/components/ui";

export function RowDrawer({
  title,
  ageBadge,
  onClose,
  returnFocusRef,
  footer,
  children,
}: {
  title: string;
  // Header age badge (§4.3: "the age badge in the header carries
  // `STALE {age}`" for the stale state) — optional, caller supplies the
  // literal text and tone.
  ageBadge?: { label: string; tone: Tone };
  onClose: () => void;
  returnFocusRef?: React.RefObject<HTMLElement | null>;
  footer?: ReactNode;
  children: ReactNode;
}) {
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    closeRef.current?.focus();
    const returnTo = returnFocusRef?.current ?? null;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      returnTo?.focus();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <>
      {/* Mobile-only scrim (< md); the desktop drawer has none (§4.3: "No
          scrim (non-modal)"). */}
      <div
        className="fixed inset-0 z-40 bg-[var(--overlay)] md:hidden"
        onClick={onClose}
        aria-hidden
      />
      <div
        role="complementary"
        aria-label={`Detail: ${title}`}
        className="fixed inset-0 z-50 flex max-w-full flex-col overflow-x-hidden bg-[var(--bg-panel)] shadow-[0_0_0_1px_var(--border),-8px_0_24px_var(--shadow-color)] md:inset-y-0 md:left-auto md:right-0 md:w-[420px] md:min-w-[360px] md:max-w-[50vw] md:border-l md:border-l-[var(--border-strong)] motion-safe:transition-transform motion-safe:duration-200"
      >
        <div className="flex h-11 shrink-0 items-center justify-between gap-2 border-b border-[var(--border)] px-3">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-[13px] font-semibold">{title}</span>
            {ageBadge && <Badge tone={ageBadge.tone}>{ageBadge.label}</Badge>}
          </div>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label="Close detail panel"
            className="flex h-8 w-8 shrink-0 items-center justify-center rounded text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
          >
            <CloseIcon />
          </button>
        </div>
        {/* min-w-0 + overflow-x-hidden: the audit's overflow came from a
            fixed label/value grid holding an unbounded decimal string
            (design-system.md §4.3) — bounded presentation (DecimalValue)
            removes the long strings at the source. Wrapping rather than
            `overflow-x-hidden`: hiding silently clipped anything wider
            than the panel — a verbatim backend reason, a long identifier
            — with no way to scroll to it. */}
        <div className="min-h-0 min-w-0 flex-1 overflow-y-auto p-4 [overflow-wrap:anywhere]">
          {children}
        </div>
        {footer && (
          <div className="flex min-h-[3rem] shrink-0 flex-wrap items-center justify-end gap-3 border-t border-[var(--border)] px-3 py-2">
            {footer}
          </div>
        )}
      </div>
    </>
  );
}
