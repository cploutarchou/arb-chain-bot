"use client";

// Shared accessibility primitives (T-087).
//
// Both of the console's mobile overlays — the navigation sheet in
// ConsoleShell and RowDrawer below the `md` breakpoint — render
// full-screen over a scrim. That is a modal presentation, and this
// refinement's own verification record listed them as missing the two
// things a modal owes a keyboard user: `aria-modal` and focus
// containment. ConfirmDialog already carries a trap of its own; rather
// than make a third copy, the logic lives here once.
//
// Two corrections over that existing copy, both of which the mobile
// navigation sheet genuinely needs:
//
//   * Unfocusable controls are excluded. `PaperControl` renders a
//     *disabled* button for a VIEWER inside that sheet, and a disabled
//     element cannot take focus — so a naive "wrap to the last match"
//     silently fails and focus walks out to the page behind the scrim,
//     which is the exact escape a trap exists to prevent.
//   * The trap is switchable. RowDrawer is deliberately non-modal on
//     desktop, where the table beside it stays interactive, and modal on
//     mobile, where it covers the screen. Containment therefore has to
//     follow the breakpoint rather than the component.

import { useEffect, useState, type RefObject } from "react";

const FOCUSABLE_SELECTOR = [
  "a[href]",
  "button",
  "input",
  "select",
  "textarea",
  "summary",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

// focusableWithin returns the descendants of `root` that can actually
// take focus at this moment, in document order.
//
// `getClientRects().length` is the visibility test rather than
// `offsetParent !== null`: the latter also reports null for a
// position:fixed element, which would wrongly drop a legitimately
// focusable control. An element inside a `display:none` subtree — a
// Settings panel hidden with the `hidden` attribute, a `md:hidden`
// overlay on a desktop viewport — has no client rects, which is the
// case that matters here.
export function focusableWithin(root: HTMLElement): HTMLElement[] {
  return Array.from(
    root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR),
  ).filter(
    (el) =>
      !el.hasAttribute("disabled") &&
      el.getAttribute("aria-hidden") !== "true" &&
      el.getClientRects().length > 0,
  );
}

// useFocusTrap keeps Tab and Shift+Tab inside `ref` while `enabled`.
//
// It deliberately does not move focus when it activates: the right
// landing target differs per overlay (a close button, the first
// navigation link, the container itself), so the caller owns that and
// this owns only containment.
export function useFocusTrap(
  ref: RefObject<HTMLElement | null>,
  enabled: boolean,
): void {
  useEffect(() => {
    if (!enabled) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Tab") return;
      const root = ref.current;
      if (!root) return;
      const items = focusableWithin(root);
      if (items.length === 0) return;
      const first = items[0]!;
      const last = items[items.length - 1]!;
      const active = document.activeElement;
      // Focus sitting outside the overlay entirely — it was never moved
      // in, or something took it — is pulled back rather than left to
      // wander into the content behind the scrim.
      if (!(active instanceof HTMLElement) || !root.contains(active)) {
        e.preventDefault();
        (e.shiftKey ? last : first).focus();
        return;
      }
      if (e.shiftKey && active === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && active === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [ref, enabled]);
}

// useMediaQuery reports whether a media query matches, starting `false`
// and correcting after mount.
//
// The initial `false` is load-bearing, not laziness: there is no
// viewport to measure during a server render, and a component that
// switches to modal behaviour on a guess would trap focus on a desktop
// for the first frame. Callers therefore phrase the query as the
// condition that *adds* behaviour — "(max-width: 767.98px)" for "this
// is the mobile presentation" — so an unknown viewport means the calmer
// of the two states.
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(false);
  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const mql = window.matchMedia(query);
    setMatches(mql.matches);
    const onChange = (e: MediaQueryListEvent) => setMatches(e.matches);
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, [query]);
  return matches;
}

// MOBILE_OVERLAY_QUERY is the JS side of Tailwind's `md` breakpoint
// (768px), named once so the two overlays cannot drift apart from each
// other or from the `md:` classes that style them.
export const MOBILE_OVERLAY_QUERY = "(max-width: 767.98px)";

// prefersReducedMotion reads the setting at call time.
//
// globals.css already neutralises CSS transitions under the same query,
// but that does not cover scripted scrolling: an explicit `behavior`
// passed to scrollIntoView overrides the CSS `scroll-behavior`
// property, so a smooth scroll asked for in JavaScript has to make this
// check itself.
export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}
