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
//   2. **Unsaved edits.** A category is mounted on first visit and then
//      kept mounted, hidden with the `hidden` attribute rather than
//      unmounted. Switching categories therefore cannot discard a draft,
//      drop a `parent_version` a section is holding, or tear down a
//      subscription — which is a stronger guarantee than warning about
//      it. The saving is real all the same: a category never visited is
//      never mounted, so the heavy platform sections cost nothing to a
//      user who only came to change their password.
//   3. **Back and forward.** Activating a category writes its anchor to
//      the URL with `replaceState`, and `hashchange` is honoured, so
//      browser history behaves and a category is linkable.

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
}: {
  categories: SettingsCategoryDef[];
}) {
  const first = categories[0]?.id ?? "account";
  const [active, setActive] = useState<SettingsCategoryId>(first);
  // Mounted-once-visited. The active category is always in this set.
  const [mounted, setMounted] = useState<Set<SettingsCategoryId>>(
    () => new Set([first]),
  );
  // The anchor a deep link asked for, consumed after the category is
  // rendered so the scroll target actually exists when we look for it.
  const pendingAnchor = useRef<string | null>(null);
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
      if (anchor) pendingAnchor.current = anchor;
    },
    [],
  );

  // Resolve the incoming hash on mount and on every hashchange, so a
  // deep link works both cold and while the page is already open.
  useEffect(() => {
    const applyHash = () => {
      const raw = window.location.hash.replace(/^#/, "");
      if (!raw) return;
      const category = settingsCategoryForAnchor(raw);
      if (!category) return; // unknown anchor: stay where we are
      const exists = categories.some((c) => c.id === category);
      if (!exists) return; // the category is not available to this user
      activate(category, raw);
    };
    applyHash();
    window.addEventListener("hashchange", applyHash);
    return () => window.removeEventListener("hashchange", applyHash);
  }, [activate, categories]);

  // After the requested category has rendered, bring its section into
  // view and move focus to it. Focus, not just scroll: a keyboard or
  // screen-reader user following a deep link must land *in* the section,
  // not at the top of the document with the section merely visible.
  useEffect(() => {
    const anchor = pendingAnchor.current;
    if (!anchor) return;
    pendingAnchor.current = null;
    const el = document.getElementById(anchor);
    if (!el) return;
    el.scrollIntoView({ block: "start", behavior: "smooth" });
    el.focus({ preventScroll: true });
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
