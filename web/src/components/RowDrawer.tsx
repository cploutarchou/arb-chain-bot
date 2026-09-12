"use client";

// RowDrawer — the right-hand row-detail panel (design-system.md §4.3, UX
// §4/§9).
//
// Modality follows the presentation rather than the component, because
// the presentation genuinely differs by width:
//
//   * At >= md it is a side panel and stays NON-modal
//     (`role="complementary"`, no aria-modal, no focus trap) — the table
//     beside it must remain interactive so an operator can keep scanning
//     while comparing one row. That is a deliberate design decision, not
//     an omission.
//   * Below md there is no room for a side panel, so it is full-screen
//     over a scrim — visually a modal dialog, and it now carries the
//     semantics to match (`role="dialog"`, `aria-modal`, focus
//     containment). Previously it announced itself as a complementary
//     region while covering the entire screen, and Tab walked a keyboard
//     user out into content they could not see behind the scrim.
//
// Focus management: moves to the close button on open, returns to
// whatever triggered the drawer (`returnFocusRef`) on close; Escape
// closes.

import { useEffect, useRef, type ReactNode } from "react";
import { CloseIcon } from "@/components/icons";
import { Badge, type Tone } from "@/components/ui";
import {
  MOBILE_OVERLAY_QUERY,
  useFocusTrap,
  useMediaQuery,
} from "@/lib/a11y";

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
  const panelRef = useRef<HTMLDivElement>(null);
  // Starts false and corrects after mount, so a desktop render can never
  // trap focus for a frame on the strength of an unmeasured viewport.
  const isModal = useMediaQuery(MOBILE_OVERLAY_QUERY);
  useFocusTrap(panelRef, isModal);

  useEffect(() => {
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      // Read the ref in the cleanup, not at mount. The screener keeps
      // one drawer mounted and swaps the row inside it, so a snapshot
      // taken on mount returned focus to the *first* row's Detail button
      // no matter which row the reader actually had open.
      //
      // react-hooks/exhaustive-deps warns about reading `.current` in a
      // cleanup and advises copying it into a variable at setup time.
      // That advice describes the defect, not the fix: the whole point
      // is that the trigger changes while this effect stays mounted, so
      // the late read is deliberate.
      // eslint-disable-next-line react-hooks/exhaustive-deps
      returnFocusRef?.current?.focus();
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
        ref={panelRef}
        role={isModal ? "dialog" : "complementary"}
        aria-modal={isModal ? true : undefined}
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
