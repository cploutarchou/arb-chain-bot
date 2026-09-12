import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { LEGACY_SETTINGS_ANCHORS, SETTINGS_SECTIONS } from "@/lib/nav";

// Anchor preservation is an explicit acceptance criterion for T-087:
// nine `#…` fragments were live before the refinement and every one must
// still activate and focus its Settings category.
//
// nav.spec.ts asserts that `SETTINGS_SECTIONS` *declares* those anchors.
// That is only half a guarantee — the declaration and the page could
// drift, leaving a metadata table that claims an anchor the page no
// longer renders. So this suite reads the page source and asserts the
// two agree exactly, in the same spirit as the route-coverage test that
// scans src/app.
//
// Reading source text rather than rendering is a deliberate trade: it
// catches drift cheaply and without a DOM, and the e2e suite is what
// proves the anchor actually scrolls and takes focus in a browser.

const SETTINGS_PAGE = join(__dirname, "..", "src", "app", "settings", "page.tsx");

function renderedAnchors(): string[] {
  const src = readFileSync(SETTINGS_PAGE, "utf8");
  const found = new Set<string>();
  // <SettingsAnchor anchor="operating-mode"> — the single wrapper every
  // deep-linkable section goes through.
  for (const m of src.matchAll(/<SettingsAnchor\s+anchor="([a-z0-9-]+)"/g)) {
    const a = m[1];
    if (a) found.add(a);
  }
  return [...found].sort();
}

test.describe("settings anchors: the page and the metadata agree", () => {
  test("every declared section is actually rendered with its anchor", () => {
    const rendered = new Set(renderedAnchors());
    const missing = SETTINGS_SECTIONS.map((s) => s.anchor).filter(
      (a) => !rendered.has(a),
    );
    expect(
      missing,
      `declared in SETTINGS_SECTIONS but not rendered by settings/page.tsx: ${missing.join(", ")}`,
    ).toEqual([]);
  });

  test("the page renders no anchor the metadata does not declare", () => {
    // The other direction matters too: an anchor rendered but not
    // declared cannot be mapped to a category, so a deep link to it
    // would silently land on the default tab.
    const declared = new Set(SETTINGS_SECTIONS.map((s) => s.anchor));
    const undeclared = renderedAnchors().filter((a) => !declared.has(a));
    expect(
      undeclared,
      `rendered by settings/page.tsx but not declared in SETTINGS_SECTIONS: ${undeclared.join(", ")}`,
    ).toEqual([]);
  });

  test("all nine pre-existing anchors are rendered by the page", () => {
    // The specific regression this guards: the nine that were live
    // before the refinement, four of them linked from elsewhere in the
    // console. Losing any one is a broken bookmark.
    const rendered = new Set(renderedAnchors());
    expect(LEGACY_SETTINGS_ANCHORS.length).toBe(9);
    for (const anchor of LEGACY_SETTINGS_ANCHORS) {
      expect(rendered.has(anchor), `#${anchor} must be rendered`).toBe(true);
    }
  });

  test("the anchors linked from inside the console are rendered", () => {
    // Grepped from web/src before the change: these four are live hrefs
    // elsewhere in the app, so they are the ones a user has actually
    // clicked.
    const rendered = new Set(renderedAnchors());
    for (const anchor of [
      "markets",
      "users",
      "notifications",
      "platform-versions",
    ]) {
      expect(rendered.has(anchor), `settings#${anchor} is linked in-app`).toBe(true);
    }
  });

  test("the page wraps sections in SettingsAnchor rather than bare divs", () => {
    // The wrapper is what carries tabIndex={-1}, which is what lets a
    // deep link move focus into the section instead of leaving a
    // keyboard user at the top of the document. A bare `<div id="…">`
    // would scroll but not focus, which is the regression this catches.
    const src = readFileSync(SETTINGS_PAGE, "utf8");
    const bareIdDivs = [...src.matchAll(/<div\s+id="([a-z0-9-]+)"/g)].map(
      (m) => m[1],
    );
    const declared = new Set(SETTINGS_SECTIONS.map((s) => s.anchor));
    const bareSectionAnchors = bareIdDivs.filter(
      (id) => id && declared.has(id),
    );
    expect(
      bareSectionAnchors,
      `these section anchors are bare <div id> and will not take focus: ${bareSectionAnchors.join(", ")}`,
    ).toEqual([]);
  });
});
