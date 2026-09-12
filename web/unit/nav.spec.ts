import { readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import {
  findLeafByHref,
  LEGACY_SETTINGS_ANCHORS,
  NAV,
  resolveNav,
  SETTINGS_SECTIONS,
  settingsCategoryForAnchor,
  type NavLeaf,
} from "@/lib/nav";

// T-087. The navigation change reduces 29 standing links to six primary
// destinations plus an operator administration area. The risk that
// matters is not aesthetic — it is losing a route, or breaking a deep
// link, or reintroducing label-keyed selection. These tests hold the
// line on all three, and the route-coverage test reads the filesystem so
// it cannot drift from the app.

const APP_DIR = join(__dirname, "..", "src", "app");

// Routes that are deliberately outside the console shell.
const OUTSIDE_SHELL = new Set(["/", "/login"]);

function discoverRoutes(dir: string, prefix = ""): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...discoverRoutes(full, `${prefix}/${entry}`));
    } else if (entry === "page.tsx") {
      out.push(prefix === "" ? "/" : prefix);
    }
  }
  return out;
}

// A dynamic segment is exercised with a concrete id, which is also the
// case that used to highlight nothing.
function concreteRoute(route: string): string {
  return route.replace(/\[[^\]]+\]/g, "sample-id-01");
}

function allLeaves(): NavLeaf[] {
  return NAV.flatMap((d) => d.groups.flatMap((g) => g.items));
}

test.describe("every route the app serves still resolves", () => {
  test("no page under src/app is orphaned by the new structure", () => {
    const routes = discoverRoutes(APP_DIR).sort();
    // Guard the guard: if this count collapses, the discovery broke.
    expect(routes.length).toBeGreaterThanOrEqual(37);

    const orphans: string[] = [];
    for (const route of routes) {
      if (OUTSIDE_SHELL.has(route)) continue;
      const match = resolveNav(concreteRoute(route));
      if (!match) orphans.push(route);
    }
    expect(orphans, `routes resolving to no navigation entry: ${orphans.join(", ")}`).toEqual([]);
  });

  test("the three pages that previously highlighted nothing now resolve", () => {
    // Each of these passed an `active` string matching no nav label, so
    // the old shell highlighted nothing and its group header stayed
    // closed. Route matching fixes all three.
    const cycle = resolveNav("/cycles/01M13T0XRW1TH3H0PAFW27V5VX");
    expect(cycle?.destination.id).toBe("paper");
    expect(cycle?.leaf.id).toBe("cycle-detail");

    const report = resolveNav("/screener-reports/42");
    expect(report?.destination.id).toBe("research");
    expect(report?.leaf.id).toBe("screener-reports");

    const onboarding = resolveNav("/onboarding");
    expect(onboarding?.destination.id).toBe("overview");
    expect(onboarding?.leaf.id).toBe("onboarding");
  });

  test("detail pages resolve to their parent entry, not to nothing", () => {
    expect(resolveNav("/triangles/abc")?.leaf.id).toBe("triangles");
    expect(resolveNav("/opportunities/abc")?.leaf.id).toBe("opportunities");
    // …and the parent list route still resolves to itself.
    expect(resolveNav("/triangles")?.leaf.id).toBe("triangles");
    expect(resolveNav("/opportunities")?.leaf.id).toBe("opportunities");
  });

  test("the longest matching route wins", () => {
    // "/screener" must not swallow "/screener-reports": prefix matching
    // is segment-aware, so a shared text prefix is not a match.
    expect(resolveNav("/screener")?.leaf.id).toBe("screener");
    expect(resolveNav("/screener-reports")?.leaf.id).toBe("screener-reports");
    expect(resolveNav("/screener-reports/7")?.leaf.id).toBe("screener-reports");
  });

  test("trailing slashes and query strings do not change the answer", () => {
    expect(resolveNav("/paper/")?.leaf.id).toBe("paper-triangular");
    expect(resolveNav("/paper?tab=history")?.leaf.id).toBe("paper-triangular");
    expect(resolveNav("/orders/?venue=binance")?.leaf.id).toBe("orders");
  });

  test("/settings resolves to Settings, not to a hash-sibling in another destination", () => {
    // Eight Operations entries have hrefs like "/settings#markets",
    // which strip to "/settings" for route matching. Without a leaf in
    // the Settings destination owning the bare path, /settings resolved
    // to Operations' "Operating mode" — so a normal user opening
    // Settings saw the Operations destination highlighted and a
    // breadcrumb reading "Operations / Operating mode".
    const m = resolveNav("/settings");
    expect(m?.destination.id).toBe("settings");
    expect(m?.leaf.id).toBe("settings-home");
  });

  test("every declared platform settings section has a navigation entry", () => {
    // #venues was declared and documented but had no entry, so it was
    // reachable only by typing the fragment.
    const leafHrefs = new Set(
      NAV.flatMap((d) => d.groups.flatMap((g) => g.items)).map((l) => l.href),
    );
    const unreachable = SETTINGS_SECTIONS.filter(
      (sec) => sec.platform && !leafHrefs.has(`/settings#${sec.anchor}`),
    ).map((sec) => sec.anchor);
    expect(
      unreachable,
      `platform settings sections with no nav entry: ${unreachable.join(", ")}`,
    ).toEqual([]);
  });

  test("the Settings destination lists routes, not in-page categories", () => {
    // The page's own tablist selects Account / Notifications /
    // Administration. Listing them in the sidebar as well is two
    // controls for one choice — the duplicate affordance the icon rail
    // was removed for. Only genuinely separate routes belong here.
    const settings = NAV.find((d) => d.id === "settings");
    const standing = settings!.groups
      .flatMap((g) => g.items)
      .filter((l) => !l.contextual)
      .map((l) => l.id);
    expect(standing).toEqual(["org", "billing"]);
    // And no standing Settings entry is a bare fragment link.
    for (const id of standing) {
      const leaf = settings!.groups.flatMap((g) => g.items).find((l) => l.id === id);
      expect(leaf?.href.includes("#"), `${id} must be a route, not a fragment`).toBe(false);
    }
  });

  test("an unknown route resolves to nothing rather than guessing", () => {
    expect(resolveNav("/does-not-exist")).toBeNull();
    expect(resolveNav("/screenerish")).toBeNull();
  });
});

test.describe("selection is keyed on stable ids, not display text", () => {
  test("every leaf and destination id is unique", () => {
    const leafIds = allLeaves().map((l) => l.id);
    expect(new Set(leafIds).size, `duplicate leaf ids in ${leafIds.join(", ")}`).toBe(leafIds.length);
    const destIds = NAV.map((d) => d.id);
    expect(new Set(destIds).size).toBe(destIds.length);
  });

  test("renaming a label does not change which entry a route resolves to", () => {
    const before = resolveNav("/screener");
    expect(before?.leaf.id).toBe("screener");
    const original = before!.leaf.label;
    try {
      // Simulate a copy change of exactly the kind that silently broke
      // the old label-keyed lookup.
      before!.leaf.label = "Cross-exchange spot differences";
      const after = resolveNav("/screener");
      expect(after?.leaf.id).toBe("screener");
      expect(after?.destination.id).toBe("discover");
    } finally {
      before!.leaf.label = original;
    }
  });
});

test.describe("primary navigation stays small and labelled", () => {
  test("there are six product destinations plus one operator area", () => {
    const product = NAV.filter((d) => d.id !== "operator").map((d) => d.id);
    expect(product).toEqual([
      "overview",
      "discover",
      "paper",
      "research",
      "alerts",
      "settings",
    ]);
    expect(NAV.map((d) => d.id)).toContain("operator");
    expect(NAV.length).toBe(7);
  });

  test("the ambiguous Scanner/Screener pair is explained, not left to intuition", () => {
    const screener = allLeaves().find((l) => l.id === "screener");
    const scanner = allLeaves().find((l) => l.id === "scanner");
    expect(screener?.description).toBeTruthy();
    expect(scanner?.description).toBeTruthy();
    expect(screener?.description).not.toBe(scanner?.description);
    // Neither label may be a bare "Scanner"/"Screener" with nothing else.
    expect(screener?.label).not.toBe("Screener");
    expect(scanner?.label).not.toBe("Scanner");
  });

  test("safety access is not traded away for a smaller nav", () => {
    const risk = allLeaves().find((l) => l.id === "risk");
    expect(risk, "the risk centre must keep a standing navigation entry").toBeTruthy();
    expect(risk?.href).toBe("/risk");
    expect(risk?.contextual).not.toBe(true);
  });

  test("every platform-configuration entry is gated on platform staff", () => {
    // The check that actually matters: platform settings are never
    // reachable on the strength of a tenant's ADMIN display role.
    const configIds = [
      "settings-operating-mode",
      "settings-markets",
      "settings-venues",
      "settings-ai",
      "settings-logging",
      "settings-users",
      "settings-security",
      "settings-versions",
    ];
    for (const id of configIds) {
      const leaf = allLeaves().find((l) => l.id === id);
      expect(leaf, `${id} must exist`).toBeTruthy();
      expect(leaf?.access, `${id} must be platform-staff only`).toEqual({
        kind: "platform",
      });
    }
    // Every platform-gated entry in the whole definition lives in the
    // operator area — none leaks into a product destination.
    for (const d of NAV) {
      for (const g of d.groups) {
        for (const leaf of g.items) {
          if (leaf.access?.kind === "platform") {
            expect(d.id, `${leaf.id} is platform-gated outside Operations`).toBe(
              "operator",
            );
          }
        }
      }
    }
  });

  test("the two ADMIN-role settings sections carry their real check, not the platform flag", () => {
    // PermScreenerConfig and PermRiskConfig are ADMIN-role permissions,
    // not platform_admin. Declaring them platform-only would hide them
    // from an ADMIN the backend would allow — a different bug from the
    // one we are guarding against, in the opposite direction.
    const scanner = allLeaves().find((l) => l.id === "settings-scanner");
    expect(scanner?.access).toEqual({
      kind: "perm",
      perm: "screener:config",
      minRoleLabel: "Requires ADMIN",
    });
    for (const anchor of ["scanner-suite", "strategy-risk"]) {
      const section = SETTINGS_SECTIONS.find((x) => x.anchor === anchor);
      expect(section, `#${anchor} must be declared`).toBeTruthy();
      expect(section?.platform, `#${anchor} is role-gated, not platform-gated`).not.toBe(true);
      expect(section?.category).toBe("administration");
    }
  });

  test("organisation administration is not platform administration", () => {
    // /org and /billing are tenant surfaces whose own pages gate
    // mutation on OWNER/ADMIN; they must not sit behind the platform
    // flag, which would hide a tenant's own organisation from them.
    const settings = NAV.find((d) => d.id === "settings");
    expect(settings?.access?.kind).not.toBe("platform");
    for (const id of ["org", "billing"]) {
      const leaf = allLeaves().find((l) => l.id === id);
      expect(leaf, `${id} must exist`).toBeTruthy();
      expect(leaf?.access?.kind, `${id} must not be platform-gated`).not.toBe(
        "platform",
      );
    }
  });

  test("the operator area itself is not platform-gated, so existing access is not removed", () => {
    // /risk, /system and /exchanges are visible to ordinary tenant roles
    // today and /audit sits behind the backend's view:audit permission.
    // Gating the whole area on platform_admin would have removed that
    // access — including safety access — to buy a smaller nav count.
    const operator = NAV.find((d) => d.id === "operator");
    expect(operator?.access?.kind).not.toBe("platform");
    for (const id of ["risk", "system", "exchanges"]) {
      const leaf = allLeaves().find((l) => l.id === id);
      expect(leaf?.access, `${id} must stay member-visible`).toBeUndefined();
    }
  });

  test("the audit log keeps its permission annotation", () => {
    const audit = allLeaves().find((l) => l.id === "audit");
    expect(audit?.access).toEqual({
      kind: "perm",
      perm: "view:audit",
      minRoleLabel: "Requires OPERATOR or ADMIN",
    });
  });
});

test.describe("settings deep links survive the recategorisation", () => {
  test("all nine pre-existing anchors are declared and mapped", () => {
    // The real list, grepped from app/settings/page.tsx before the change.
    const existing = [
      "operating-mode",
      "markets",
      "scanner-suite",
      "logging",
      "ai",
      "platform-versions",
      "users",
      "notifications",
      "security",
    ];
    for (const anchor of existing) {
      const section = SETTINGS_SECTIONS.find((s) => s.anchor === anchor);
      expect(section, `anchor #${anchor} must still be declared`).toBeTruthy();
      expect(section?.legacy, `anchor #${anchor} must be marked legacy`).toBe(true);
      expect(
        settingsCategoryForAnchor(anchor),
        `anchor #${anchor} must activate a category`,
      ).toBeTruthy();
    }
    expect(LEGACY_SETTINGS_ANCHORS.sort()).toEqual([...existing].sort());
  });

  test("the anchors linked from inside the console resolve", () => {
    // These four are live hrefs elsewhere in web/src today.
    expect(settingsCategoryForAnchor("#markets")).toBe("administration");
    expect(settingsCategoryForAnchor("#users")).toBe("administration");
    expect(settingsCategoryForAnchor("#platform-versions")).toBe("administration");
    expect(settingsCategoryForAnchor("#notifications")).toBe("notifications");
  });

  test("account and notifications are tenant categories, not administration", () => {
    expect(settingsCategoryForAnchor("account")).toBe("account");
    expect(settingsCategoryForAnchor("notifications")).toBe("notifications");
    expect(SETTINGS_SECTIONS.find((s) => s.anchor === "account")?.platform).not.toBe(true);
    expect(SETTINGS_SECTIONS.find((s) => s.anchor === "notifications")?.platform).not.toBe(true);
  });

  test("every platform settings section is flagged platform-only", () => {
    // Scanner Suite and Strategy & risk are deliberately absent: the
    // backend gates those on the global ADMIN role (PermScreenerConfig /
    // PermRiskConfig), not on platform_admin, so marking them
    // platform-only would hide them from an ADMIN the backend allows.
    const platformAnchors = [
      "operating-mode",
      "markets",
      "venues",
      "ai",
      "logging",
      "users",
      "security",
      "platform-versions",
    ];
    for (const anchor of platformAnchors) {
      const s = SETTINGS_SECTIONS.find((x) => x.anchor === anchor);
      expect(s?.platform, `#${anchor} must be platform-only`).toBe(true);
      expect(s?.category).toBe("administration");
    }
  });

  test("an unknown anchor falls back to the default category, not a blank page", () => {
    expect(settingsCategoryForAnchor("#nope")).toBeNull();
    expect(settingsCategoryForAnchor(null)).toBeNull();
    expect(settingsCategoryForAnchor(undefined)).toBeNull();
  });
});

// A fragment names a sub-view and can move the answer to a different
// destination than the bare path does. Eight Operations entries point at
// `/settings#<anchor>`, and while selection resolved on the path alone
// every one of them highlighted Settings instead — emptying the list
// they were clicked from and leaving the activated entry with no current
// state anywhere. These tests pin both halves of the fix: the path-only
// function is total, and the exact-href lookup finds the real owner.
test.describe("fragment-bearing navigation entries resolve to their own destination", () => {
  const fragmented = NAV.flatMap((d) =>
    d.groups.flatMap((g) =>
      g.items
        .filter((l) => l.href.includes("#"))
        .map((l) => ({ destination: d, leaf: l })),
    ),
  );

  test("there are fragment-bearing entries to protect", () => {
    // If this ever reaches zero the tests below stop proving anything,
    // so the premise is asserted rather than assumed.
    expect(fragmented.length).toBeGreaterThan(0);
  });

  test("each is found by its exact href, under the destination that owns it", () => {
    for (const { destination, leaf } of fragmented) {
      const m = findLeafByHref(leaf.href);
      expect(m, `${leaf.href} must be findable by exact href`).not.toBeNull();
      expect(m?.leaf.id).toBe(leaf.id);
      expect(
        m?.destination.id,
        `${leaf.href} must resolve to ${destination.id}, not to whichever destination owns the bare path`,
      ).toBe(destination.id);
    }
  });

  test("the bare path alone resolves elsewhere — which is exactly why the fragment is consulted first", () => {
    for (const { destination, leaf } of fragmented) {
      const path = leaf.href.split("#")[0]!;
      const byPath = resolveNav(path);
      // Not an assertion that this is wrong — it is correct for the bare
      // path. It documents that the two answers genuinely differ, so a
      // future change cannot quietly drop the fragment step.
      if (byPath && byPath.destination.id !== destination.id) {
        expect(byPath.leaf.id).not.toBe(leaf.id);
      }
    }
  });

  test("resolveNav strips a fragment instead of returning null", () => {
    // It used to match on the raw string, so any path carrying a
    // fragment scored nothing and highlighted nothing.
    for (const { leaf } of fragmented) {
      const m = resolveNav(leaf.href);
      expect(m, `resolveNav(${leaf.href}) must not be null`).not.toBeNull();
    }
    expect(resolveNav("/settings#markets")?.destination.id).toBe(
      resolveNav("/settings")?.destination.id,
    );
    expect(resolveNav("/risk?x=1#y")?.destination.id).toBe(
      resolveNav("/risk")?.destination.id,
    );
  });
});

test.describe("settings categories are the three the page builds", () => {
  test("no section claims a category the page does not render", () => {
    // `organisation` and `billing` were declared as in-page categories
    // that nothing mapped to, which is where the "five categories by
    // audience" claim came from. They are separate routes.
    const used = new Set(SETTINGS_SECTIONS.map((s) => s.category));
    expect([...used].sort()).toEqual([
      "account",
      "administration",
      "notifications",
    ]);
  });

  test("/org and /billing remain their own routes", () => {
    expect(resolveNav("/org")).not.toBeNull();
    expect(resolveNav("/billing")).not.toBeNull();
  });
});
