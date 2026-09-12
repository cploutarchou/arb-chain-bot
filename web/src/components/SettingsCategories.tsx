"use client";

// SettingsCategories — the category shell for /settings (T-087).
//
// The audit found Settings as one continuous form mixing a setup nudge,
// the signed-in session, engine operating mode and markets/assets before
// the fold, with every further administrative section stacked below it
// in one column. Nothing was broken; the page simply had no audience
// boundary, so an account question and a platform-wide engine setting sat
// in the same scroll.
//
// This splits it by **audience** — Account, Organisation, Billing,
// Notifications, Administration — and keeps three things working that a
// naive tab rewrite would break:
//
//   1. **Every existing deep link.** Nine anchors were live before this
//      change (#operating-mode #markets #scanner-suite #logging #ai
//      #platform-versions #users #notifications #security), four of them
//      linked from elsewhere in the console. A link to any of them must
//      activate the right category, scroll to the section and move focus
//      there — not silently land on a default tab. `LEGACY_SETTINGS_ANCHORS`
//      in lib/nav.ts is the contract and the e2e suite asserts it.
//      The contract is bounded by permission: an anchor whose category
//      this reader may not open cannot be honoured, and that case is
//      *said* rather than silently absorbed into the default tab — which
//      is what master did at the anchor itself and what the first
//      version of this component dropped.
//   2. **Unsaved edits.** A category is mounted on first visit and then
//      kept mounted, hidden with the `hidden` attribute rather than
//      unmounted. Switching categories therefore cannot discard a draft,
//      drop a `parent_version` a section is holding, or tear down a
//      subscription — which is a stronger guarantee than warning about
//      it. The saving is real all the same: a category never visited is
//      never mounted, so the heavy platform sections cost nothing to a
//      user who only came to change their password.
//   3. **A hash that can disagree with the tabs.** `hashchange` is
//      honoured, so a link to a section works while the page is already
//      open. Clicking a tab deliberately does *not* rewrite the URL: no
//      history entry is manufactured for a click, and the address a
//      reader arrived with is left as they typed or bookmarked it. The
//      consequence has to be handled rather than ignored — the hash can
//      name a category the reader has since navigated away from, so the
//      hash is applied only when the *set of available categories*
//      changes, never on an incidental re-render of the page above.
//      (Keying that effect on the `categories` array identity, which is
//      a fresh literal every parent render, made any re-render snap the
//      reader back to the deep-linked category mid-task.)

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  settingsCategoryForAnchor,
  type SettingsCategoryId,
} from "@/lib/nav";
import { prefersReducedMotion } from "@/lib/a11y";

// ANCHOR_DEADLINE_MS bounds how long a deep link may still claim focus.
// The panel it names normally commits on the very next render, so this is
// generous — but a section that only appears seconds later must not yank
// the caret out of whatever the reader has started doing by then.
const ANCHOR_DEADLINE_MS = 3000;

export interface SettingsCategoryDef {
  id: SettingsCategoryId;
  label: string;
  // description: what belongs in here, so the choice is informed before
  // it is made rather than after.
  description: string;
  content: ReactNode;
}

export function SettingsCategories({
  categories,
  ready = true,
}: {
  categories: SettingsCategoryDef[];
  // ready: the category list is final, i.e. the session has resolved.
  // While auth is still loading the list is legitimately short, and an
  // anchor for a category that has not appeared *yet* must not be
  // reported as one the reader may not open.
  ready?: boolean;
}) {
  const first = categories[0]?.id ?? "account";
  const [active, setActive] = useState<SettingsCategoryId>(first);
  // Mounted-once-visited. The active category is always in this set.
  const [mounted, setMounted] = useState<Set<SettingsCategoryId>>(
    () => new Set([first]),
  );
  // The anchor a deep link asked for, consumed only once the category has
  // rendered and focus has actually landed on it.
  const pendingAnchor = useRef<string | null>(null);
  const pendingSince = useRef(0);
  // An anchor this reader's permissions do not reach. Named rather than
  // swallowed: landing on Account with the URL still reading #users and
  // no explanation anywhere is the failure master did not have.
  const [unreachableAnchor, setUnreachableAnchor] = useState<string | null>(
    null,
  );
  const tabRefs = useRef<Record<string, HTMLButtonElement | null>>({});

  const activate = useCallback(
    (id: SettingsCategoryId, anchor?: string) => {
      setActive(id);
      setMounted((prev) => {
        if (prev.has(id)) return prev;
        const next = new Set(prev);
        next.add(id);
        return next;
      });
      if (anchor) {
        pendingAnchor.current = anchor;
        pendingSince.current = performance.now();
      }
    },
    [],
  );

  // Resolve the incoming hash on mount and on every hashchange, so a
  // deep link works both cold and while the page is already open.
  //
  // The dependency is a derived *key*, not the `categories` array: the
  // page above rebuilds that array on every one of its renders, so
  // depending on its identity re-ran this effect constantly and let a
  // stale hash overrule a tab the reader had since chosen. Keying on the
  // available ids re-runs it exactly when availability genuinely changes
  // — which is the case that matters, because the first render happens
  // while the session is still loading and the category a deep link
  // wants may not exist yet.
  const categoryIds = categories.map((c) => c.id).join(",");
  useEffect(() => {
    const applyHash = () => {
      const raw = window.location.hash.replace(/^#/, "");
      if (!raw) return;
      const category = settingsCategoryForAnchor(raw);
      if (!category) return; // unknown anchor: stay where we are
      const available = categoryIds.split(",");
      if (!available.includes(category)) {
        // Not available. While the session is still resolving that is
        // simply "not yet" and this effect runs again when it lands;
        // once it has resolved, it is a permission answer and is said.
        if (ready) setUnreachableAnchor(raw);
        return;
      }
      setUnreachableAnchor(null);
      activate(category as SettingsCategoryId, raw);
    };
    applyHash();
    window.addEventListener("hashchange", applyHash);
    return () => window.removeEventListener("hashchange", applyHash);
  }, [activate, categoryIds, ready]);

  // After the requested category has rendered, bring its section into
  // view and move focus to it. Focus, not just scroll: a keyboard or
  // screen-reader user following a deep link must land *in* the section,
  // not at the top of the document with the section merely visible.
  //
  // This effect deliberately has no dependency array: it must run after
  // *every* commit, because the commit that can satisfy the request is
  // not the one that makes it. `activate()` is called from the hash
  // effect above, and React does not re-render between the passive
  // effects of a single commit — so the first time this runs, the panel
  // holding the anchor has not been committed yet and the element does
  // not exist. Consuming the request at that point was the defect: the
  // retry that would have succeeded on the next commit never happened,
  // so every cold deep link activated the right tab and then scrolled
  // nowhere and focused nothing.
  useEffect(() => {
    const anchor = pendingAnchor.current;
    if (!anchor) return;
    // Give up before looking, not after: an anchor whose section appears
    // late (a slow platform poll) must be abandoned rather than
    // satisfied, or a deep link turns into focus theft seconds later.
    if (performance.now() - pendingSince.current > ANCHOR_DEADLINE_MS) {
      pendingAnchor.current = null;
      return;
    }
    const el = document.getElementById(anchor);
    if (!el) return; // not committed yet — the next commit tries again
    el.scrollIntoView({
      block: "start",
      // An explicit `behavior` overrides the CSS `scroll-behavior`
      // property, so globals.css's reduced-motion block does not cover a
      // scripted scroll and this has to make the check itself.
      behavior: prefersReducedMotion() ? "auto" : "smooth",
    });
    el.focus({ preventScroll: true });
    // Only consume the request once focus actually arrived. `focus()` is
    // a silent no-op on an element inside a `hidden` subtree, which
    // would otherwise burn the retry having achieved nothing — the same
    // failure class as clearing it too early.
    const landed = document.activeElement;
    if (landed && el.contains(landed)) pendingAnchor.current = null;
  });

  const onTabKey = (e: React.KeyboardEvent, index: number) => {
    const keys = ["ArrowRight", "ArrowLeft", "Home", "End"];
    if (!keys.includes(e.key)) return;
    e.preventDefault();
    const last = categories.length - 1;
    const next =
      e.key === "ArrowRight"
        ? index === last
          ? 0
          : index + 1
        : e.key === "ArrowLeft"
          ? index === 0
            ? last
            : index - 1
          : e.key === "Home"
            ? 0
            : last;
    const target = categories[next];
    if (!target) return;
    activate(target.id);
    tabRefs.current[target.id]?.focus();
  };

  const activeDef = categories.find((c) => c.id === active);

  return (
    <div>
      {unreachableAnchor && (
        // Named, with the reason, rather than a silent landing on the
        // default tab. `role="status"` and not an alert: the reader
        // asked for this by following a link, so it is an answer, not an
        // interruption.
        <p
          role="status"
          className="mb-3 rounded border border-[var(--border)] border-l-[3px] border-l-[var(--warn)] bg-[var(--bg-panel)] px-3 py-2 text-[13px] text-[var(--text)]"
        >
          The settings section you linked to (
          <span className="font-mono text-[12px]">#{unreachableAnchor}</span>) is
          not available to your role, so this page opened at{" "}
          {categories[0]?.label ?? "the first category"} instead.
        </p>
      )}
      <div
        role="tablist"
        aria-label="Settings categories"
        className="mb-1 flex flex-wrap gap-1 border-b border-[var(--border)]"
      >
        {categories.map((c, i) => {
          const selected = c.id === active;
          return (
            <button
              key={c.id}
              ref={(el) => {
                tabRefs.current[c.id] = el;
              }}
              type="button"
              role="tab"
              id={`settings-tab-${c.id}`}
              aria-selected={selected}
              aria-controls={`settings-panel-${c.id}`}
              // Roving tabindex: one stop for the whole tablist, arrows
              // move between tabs — the expected tab-widget behaviour.
              tabIndex={selected ? 0 : -1}
              onClick={() => activate(c.id)}
              onKeyDown={(e) => onTabKey(e, i)}
              title={c.description}
              className={`-mb-px rounded-t border-b-2 px-3 py-1.5 text-[13px] ${
                selected
                  ? "border-[var(--accent)] font-medium text-[var(--text)]"
                  : "border-transparent text-[var(--text-dim)] hover:text-[var(--text)]"
              }`}
            >
              {c.label}
            </button>
          );
        })}
      </div>
      {activeDef && (
        <p className="mb-4 mt-2 max-w-2xl text-[12px] text-[var(--text-dim)]">
          {activeDef.description}
        </p>
      )}
      {categories.map((c) =>
        mounted.has(c.id) ? (
          <div
            key={c.id}
            role="tabpanel"
            id={`settings-panel-${c.id}`}
            aria-labelledby={`settings-tab-${c.id}`}
            // `hidden` rather than unmounting: the panel keeps its state,
            // its drafts and its subscriptions while it is not on screen.
            hidden={c.id !== active}
          >
            {c.content}
          </div>
        ) : null,
      )}
    </div>
  );
}

// SettingsSection wraps one deep-linkable section. The id is the anchor,
// and tabIndex={-1} makes it a focus target so following a deep link
// lands the caret inside the section rather than leaving a keyboard user
// at the top of the page.
export function SettingsAnchor({
  anchor,
  children,
}: {
  anchor: string;
  children: ReactNode;
}) {
  return (
    <div id={anchor} tabIndex={-1} className="scroll-mt-4 outline-none">
      {children}
    </div>
  );
}
