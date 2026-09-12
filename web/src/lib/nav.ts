// The console's single navigation definition (T-087).
//
// Why this file exists at all. The previous shell held a `GROUPS` array
// inside ConsoleShell.tsx listing 29 links across six groups, all
// expanded at once, beside a second icon rail offering the same six
// choices. It decided which entry was current by comparing an `active:
// string` prop against the entry's **display label**, and looked up each
// glyph by that same label — so renaming any visible text silently broke
// both the highlight and the icon. Three pages were already broken that
// way before this change: /cycles/[id] passed "Paper" (label: "Paper
// Trading"), /screener-reports/* passed "Screener Reports" (label:
// "Evidence — Screener Reports"), and /onboarding passed "Onboarding"
// (no such label).
//
// So: every entry carries a stable `id`, the current entry is resolved
// from the URL by route/segment matching, and display text is free to
// change without touching selection, icons or tests.
//
// The structure is six primary destinations plus an explicitly labelled
// operator administration area. Every route the console has ever served
// still resolves here — a destination is a *grouping of surfaces*, not a
// gate. Routes reachable only in context (detail pages, the calculator
// hand-off, the setup wizard) are marked `contextual` so they resolve
// for breadcrumbs and highlighting without adding another standing link.
//
// One definition serves desktop and mobile; there is no second list to
// keep in sync.

// ---- Access rules --------------------------------------------------------
// These mirror the backend, which remains the only real gate (AGENTS.md:
// "RBAC is enforced in the backend, never by hiding buttons"). Anything
// hidden here is also refused server-side; anything shown here may still
// be refused server-side. Where an entry is visible but restricted, the
// shell renders an honest explanation rather than a link that 403s.

export type NavAccess =
  // open to any authenticated member of the organisation
  | { kind: "member" }
  // needs a backend permission, checked through lib/auth's `can()`
  | { kind: "perm"; perm: string; minRoleLabel: string }
  // platform staff only: the `platform_admin` flag on /auth/me, NEVER a
  // tenant's ADMIN display role. An organisation admin administers their
  // organisation; they do not administer the platform.
  | { kind: "platform" }
  // gated by the organisation's resolved entitlements document
  | { kind: "entitlement"; key: "auto_paper.strategies"; packageName: string };

export interface NavLeaf {
  // id: stable across renames. Tests, icons and analytics key off this.
  id: string;
  label: string;
  href: string;
  // description: one plain-language line. The audit found "Scanner"
  // versus "Screener" undecipherable without explanation, so every
  // ambiguous entry states what it actually shows.
  description?: string;
  // match: additional route prefixes belonging to this entry, so detail
  // pages highlight their parent and appear in its breadcrumb trail.
  match?: string[];
  access?: NavAccess;
  // contextual: resolvable and breadcrumb-able, but not rendered as a
  // standing navigation link — it is reached from the surface that hands
  // off to it. Its URL keeps working exactly as before.
  contextual?: boolean;
}

export interface NavGroup {
  // title: optional sub-heading inside a destination's secondary nav.
  title?: string;
  items: NavLeaf[];
}

export interface NavDestination {
  id: NavDestinationId;
  label: string;
  // href: where the destination itself lands.
  href: string;
  description: string;
  // icon: a key into the glyph table in components/icons.tsx. Those
  // glyphs were drawn against the previous navigation's display labels,
  // so referencing them by key here preserves every existing icon
  // without a second copy of the artwork and without the icon lookup
  // going back to being keyed on visible text.
  icon: string;
  groups: NavGroup[];
  access?: NavAccess;
}

export type NavDestinationId =
  | "overview"
  | "discover"
  | "paper"
  | "research"
  | "alerts"
  | "settings"
  | "operator";

// ---- The definition ------------------------------------------------------

export const NAV: NavDestination[] = [
  {
    id: "overview",
    label: "Overview",
    icon: "Overview",
    href: "/overview",
    description: "Platform status, what needs attention, and simulation results",
    groups: [
      {
        items: [
          { id: "overview", label: "Overview", href: "/overview" },
          {
            id: "onboarding",
            label: "Setup wizard",
            href: "/onboarding",
            description: "Pick venues, set simulated balances, create a rule",
            contextual: true,
          },
        ],
      },
    ],
  },
  {
    id: "discover",
    label: "Discover",
    icon: "Screener",
    href: "/screener",
    description: "Find candidate opportunities across venues",
    groups: [
      {
        title: "Cross-exchange",
        items: [
          {
            id: "screener",
            label: "Spot screener",
            href: "/screener",
            // The audit's §2 finding: the user should not have to work
            // out what distinguishes this from the triangular scanner.
            description:
              "Buy on one exchange, sell on another — spot price differences between venues",
          },
          {
            id: "perpetuals",
            label: "Perpetuals",
            href: "/perpetuals",
            description: "Basis between perpetual futures and spot",
          },
          {
            id: "funding",
            label: "Funding",
            href: "/funding",
            description: "Funding-rate carry on perpetual positions",
          },
        ],
      },
      {
        title: "Triangular",
        items: [
          {
            id: "scanner",
            label: "Triangular scanner",
            href: "/scanner",
            description:
              "Three trades within one exchange returning to the starting asset",
          },
          {
            id: "triangles",
            label: "Triangles",
            href: "/triangles",
            match: ["/triangles/"],
            description: "The tradable three-leg paths being monitored",
          },
          {
            id: "opportunities",
            label: "Opportunities",
            href: "/opportunities",
            match: ["/opportunities/"],
            description: "Triangular opportunities detected so far",
          },
        ],
      },
      {
        items: [
          {
            id: "calculator",
            label: "Spreads calculator",
            href: "/calculator",
            description:
              "Estimate a specific trade's costs and feasibility at a chosen size",
          },
        ],
      },
    ],
  },
  {
    id: "paper",
    label: "Paper Trading",
    icon: "Paper Trading",
    href: "/paper",
    description: "Simulated execution — no real orders are ever placed",
    groups: [
      {
        title: "Simulations",
        items: [
          {
            id: "paper-triangular",
            label: "Triangular simulations",
            href: "/paper",
            description:
              "Simulated three-leg cycles from the triangular engine",
          },
          {
            id: "paper-rules",
            label: "Rule simulations",
            href: "/auto-paper",
            description:
              "Simulated trades your alert rules opened automatically",
            // The UX spec's own package-gating example: an organisation
            // whose entitlements list no auto-paper strategies sees this
            // as gated with an upgrade explanation, not a dead link.
            access: {
              kind: "entitlement",
              key: "auto_paper.strategies",
              packageName: "Signal",
            },
          },
          {
            id: "cycle-detail",
            label: "Cycle detail",
            href: "/cycles",
            match: ["/cycles/"],
            contextual: true,
          },
        ],
      },
      {
        title: "Position and ledger",
        items: [
          {
            id: "portfolio",
            label: "Balances",
            href: "/portfolio",
            description: "Simulated balances and what is reserved",
          },
          { id: "orders", label: "Orders", href: "/orders" },
          { id: "fills", label: "Fills", href: "/fills" },
        ],
      },
    ],
  },
  {
    id: "research",
    label: "Research & Results",
    icon: "Reports",
    href: "/pnl",
    description: "Simulated results, evidence and their limitations",
    groups: [
      {
        title: "Results",
        items: [
          {
            id: "pnl",
            label: "Results & analytics",
            href: "/pnl",
            description: "Simulated profit and loss after fees, by asset",
          },
        ],
      },
      {
        // The two report systems are genuinely different and must never
        // be presented as one (docs/user-guide/reports.md).
        title: "Evidence",
        items: [
          {
            id: "screener-reports",
            label: "Screener evidence",
            href: "/screener-reports",
            match: ["/screener-reports/"],
            description:
              "Paper evidence from the cross-exchange screener, with sample size and window",
          },
          {
            id: "reports",
            label: "Engine reports",
            href: "/reports",
            description:
              "Daily and weekly operations reports from the trading engine",
            access: {
              kind: "perm",
              perm: "reports:view",
              minRoleLabel: "Requires VIEWER or above",
            },
          },
          {
            id: "campaigns",
            label: "Campaigns",
            href: "/campaigns",
            description: "Validation runs and their recorded verdicts",
          },
        ],
      },
      {
        title: "Investigate",
        items: [
          {
            id: "replay",
            label: "Replay & backtesting",
            href: "/replay",
            description: "Re-run recorded market data against the engine",
          },
          {
            id: "ai",
            label: "AI advisor",
            href: "/ai",
            description: "Suggested parameter changes, applied only on approval",
          },
        ],
      },
    ],
  },
  {
    id: "alerts",
    label: "Alerts & Rules",
    icon: "Alerts",
    href: "/alerts",
    description: "What the platform told you, and what you told it to watch for",
    groups: [
      {
        // Incident alerts and rule configuration stay separate: one is
        // the platform reporting a problem, the other is the user
        // configuring a watch. Merging them loses that distinction.
        title: "Notifications",
        items: [
          {
            id: "alerts",
            label: "Platform alerts",
            href: "/alerts",
            description:
              "Incidents the platform raised: feed problems, breakers, guards",
          },
        ],
      },
      {
        title: "Configuration",
        items: [
          {
            id: "scanner-alerts",
            label: "Alert rules",
            href: "/scanner-alerts",
            description: "Conditions you want to be told about",
          },
          {
            id: "strategies",
            label: "Strategies",
            href: "/strategies",
            description: "Strategy parameters and their per-strategy ledgers",
          },
          {
            id: "telegram",
            label: "Telegram",
            href: "/telegram",
            description: "Where alerts are delivered",
          },
        ],
      },
    ],
  },
  {
    id: "settings",
    label: "Settings",
    icon: "Settings",
    href: "/settings",
    description: "Your account, your organisation, and how you are billed",
    groups: [
      {
        items: [
          {
            id: "settings-account",
            label: "Account",
            href: "/settings#account",
            description: "Your sign-in, password and session",
          },
          {
            id: "settings-notifications",
            label: "Notifications",
            href: "/settings#notifications",
            description: "How and when you are notified",
          },
          {
            id: "org",
            label: "Organisation",
            href: "/org",
            description: "Members, roles and seats in your organisation",
          },
          {
            id: "billing",
            label: "Billing",
            href: "/billing",
            description: "Your package, invoices and payment details",
          },
        ],
      },
    ],
  },
  {
    // Operations: the explicitly labelled operator area holding safety,
    // platform health and platform configuration.
    //
    // This destination is deliberately NOT gated as a whole on
    // platform_admin. /risk, /system, /exchanges and /audit are visible
    // to ordinary tenant roles today — /audit behind the backend's
    // view:audit permission (OPERATOR/ADMIN), the other three to any
    // member — and hiding the area behind a platform-staff flag would
    // have *removed* access that exists, including safety access, to buy
    // a smaller navigation count. Existing authorization semantics are
    // preserved exactly: each entry carries the check it already had.
    //
    // What IS platform-staff only is the platform *configuration* group
    // below. An organisation admin administers their organisation
    // (Settings › Organisation); they do not configure the platform, and
    // an ADMIN display role alone never opens these.
    id: "operator",
    label: "Operations",
    icon: "System Health",
    href: "/risk",
    description: "Safety, platform health, and platform configuration",
    groups: [
      {
        title: "Safety",
        items: [
          {
            id: "risk",
            label: "Risk centre",
            href: "/risk",
            description: "Breakers, guards and capital limits",
          },
        ],
      },
      {
        title: "Platform health",
        items: [
          {
            id: "system",
            label: "System health",
            href: "/system",
            description: "Feed, engine, storage and queue health",
          },
          {
            id: "exchanges",
            label: "Venues",
            href: "/exchanges",
            description: "Connected exchanges and what each supports",
          },
          {
            id: "audit",
            label: "Audit log",
            href: "/audit",
            access: {
              kind: "perm",
              perm: "view:audit",
              minRoleLabel: "Requires OPERATOR or ADMIN",
            },
          },
        ],
      },
      {
        // Every entry here is platform-staff only, checked against the
        // platform_admin flag on /auth/me and enforced again by the
        // backend on each endpoint.
        title: "Platform configuration",
        items: [
          {
            id: "settings-operating-mode",
            label: "Operating mode",
            href: "/settings#operating-mode",
            access: { kind: "platform" },
          },
          {
            id: "settings-markets",
            label: "Markets & assets",
            href: "/settings#markets",
            access: { kind: "platform" },
          },
          {
            // Scanner Suite mutations are gated on PermScreenerConfig —
            // the global ADMIN role, not platform_admin
            // (internal/auth/rbac.go:59-67). Declaring it platform-only
            // here would hide it from an ADMIN the backend would in fact
            // allow, so it carries its real check instead.
            id: "settings-scanner",
            label: "Scanner Suite",
            href: "/settings#scanner-suite",
            access: {
              kind: "perm",
              perm: "screener:config",
              minRoleLabel: "Requires ADMIN",
            },
          },
          {
            id: "settings-ai",
            label: "AI settings",
            href: "/settings#ai",
            access: { kind: "platform" },
          },
          {
            id: "settings-logging",
            label: "Logging & access",
            href: "/settings#logging",
            access: { kind: "platform" },
          },
          {
            id: "settings-users",
            label: "Users & roles",
            href: "/settings#users",
            access: { kind: "platform" },
          },
          {
            id: "settings-security",
            label: "Vault & security",
            href: "/settings#security",
            access: { kind: "platform" },
          },
          {
            id: "settings-versions",
            label: "Settings history",
            href: "/settings#platform-versions",
            description: "Versions, rollback and restart state",
            access: { kind: "platform" },
          },
        ],
      },
    ],
  },
];

// ---- Resolution ----------------------------------------------------------

export interface NavMatch {
  destination: NavDestination;
  leaf: NavLeaf;
  group?: NavGroup;
}

function leafRoutes(leaf: NavLeaf): string[] {
  // A leaf's own href contributes its path (the hash is a Settings
  // category, not a route) plus any extra prefixes it claims.
  const own = leaf.href.split("#")[0] ?? leaf.href;
  return [own, ...(leaf.match ?? [])];
}

// routeScore returns how specifically `route` claims `pathname`, or -1.
// Longest match wins, so "/triangles/" beats "/triangles" for
// "/triangles/abc", and an exact hit always beats a prefix.
function routeScore(route: string, pathname: string): number {
  if (route === "" || route === "/") return pathname === "/" ? 1 : -1;
  if (pathname === route) return route.length + 1;
  const prefix = route.endsWith("/") ? route : `${route}/`;
  return pathname.startsWith(prefix) ? route.length : -1;
}

// resolveNav finds the entry that owns a pathname. Route-based, so a
// label rename cannot change the answer, and a detail page resolves to
// its parent entry instead of highlighting nothing.
export function resolveNav(pathname: string): NavMatch | null {
  // Trailing slashes and query/hash are not part of the decision.
  const path = (pathname.split("?")[0] ?? pathname).replace(/\/+$/, "") || "/";
  let best: NavMatch | null = null;
  let bestScore = 0;
  for (const destination of NAV) {
    for (const group of destination.groups) {
      for (const leaf of group.items) {
        for (const route of leafRoutes(leaf)) {
          const score = routeScore(route, path);
          if (score > bestScore) {
            bestScore = score;
            best = { destination, leaf, group };
          }
        }
      }
    }
  }
  return best;
}

// SETTINGS_CATEGORIES: the Settings page's own category structure, and
// the mapping every preserved deep link goes through. The nine anchors
// that existed before this change are listed explicitly and each one
// must still activate and focus its category — that is asserted in the
// e2e suite, not merely intended.
export type SettingsCategoryId =
  | "account"
  | "organisation"
  | "billing"
  | "notifications"
  | "administration";

export interface SettingsSectionRef {
  // anchor: the fragment, without "#". Pre-existing anchors are marked
  // `legacy` so nothing silently drops one.
  anchor: string;
  label: string;
  category: SettingsCategoryId;
  legacy?: boolean;
  // platform: rendered only for platform staff.
  platform?: boolean;
}

export const SETTINGS_SECTIONS: SettingsSectionRef[] = [
  { anchor: "account", label: "Account & session", category: "account" },
  { anchor: "notifications", label: "Notifications", category: "notifications", legacy: true },
  { anchor: "operating-mode", label: "Operating mode", category: "administration", legacy: true, platform: true },
  { anchor: "markets", label: "Markets & assets", category: "administration", legacy: true, platform: true },
  { anchor: "venues", label: "Venues & fees", category: "administration", platform: true },
  // platform: false — ADMIN-role gated (PermScreenerConfig), not
  // platform_admin gated. See the nav entry above.
  { anchor: "scanner-suite", label: "Scanner Suite", category: "administration", legacy: true },
  { anchor: "ai", label: "AI settings", category: "administration", legacy: true, platform: true },
  { anchor: "logging", label: "Logging & access", category: "administration", legacy: true, platform: true },
  { anchor: "users", label: "Users & roles", category: "administration", legacy: true, platform: true },
  { anchor: "security", label: "Vault & security", category: "administration", legacy: true, platform: true },
  { anchor: "platform-versions", label: "Settings history", category: "administration", legacy: true, platform: true },
  // platform: false — ADMIN-role gated (PermRiskConfig).
  { anchor: "strategy-risk", label: "Strategy & risk", category: "administration" },
];

// settingsCategoryForAnchor maps a deep link to the category that must
// become active. An unknown anchor resolves to null so the page opens
// its default category rather than rendering nothing.
export function settingsCategoryForAnchor(
  anchor: string | null | undefined,
): SettingsCategoryId | null {
  if (!anchor) return null;
  const clean = anchor.replace(/^#/, "");
  return SETTINGS_SECTIONS.find((s) => s.anchor === clean)?.category ?? null;
}

// LEGACY_SETTINGS_ANCHORS is the explicit contract: these nine fragments
// were live before the refinement (four of them linked from inside the
// console) and every one must keep working.
export const LEGACY_SETTINGS_ANCHORS = SETTINGS_SECTIONS.filter(
  (s) => s.legacy,
).map((s) => s.anchor);
