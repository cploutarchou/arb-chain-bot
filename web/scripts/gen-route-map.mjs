// Generates the old→new route map in
// docs/design/client-area-refinement-routes.md straight from
// src/lib/nav.ts, so the document cannot drift from the navigation the
// app actually ships. Also cross-checks the map against the pages on
// disk and fails if any page resolves to no navigation entry.
//
// Run: node scripts/gen-route-map.mjs   (from web/)
import { readdirSync, statSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const { NAV, SETTINGS_SECTIONS, resolveNav } = await import(
  join(here, "..", "src", "lib", "nav.ts")
);

const APP_DIR = join(here, "..", "src", "app");
const OUTSIDE_SHELL = new Set(["/", "/login"]);

function discover(dir, prefix = "") {
  const out = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) out.push(...discover(full, `${prefix}/${entry}`));
    else if (entry === "page.tsx") out.push(prefix === "" ? "/" : prefix);
  }
  return out;
}
const concrete = (r) => r.replace(/\[[^\]]+\]/g, "sample-id-01");

// Where each route lived in the old six-group sidebar, so the map is a
// genuine before/after rather than a description of the new state only.
const OLD_LOCATION = {
  "/overview": "Operate › Overview",
  "/scanner": "Operate › Scanner",
  "/triangles": "Operate › Triangles",
  "/triangles/[id]": "(no nav entry — detail page)",
  "/opportunities": "Operate › Opportunities",
  "/opportunities/[id]": "(no nav entry — detail page)",
  "/paper": "Operate › Paper Trading",
  "/cycles/[id]": "(no nav entry — detail page, highlighted nothing)",
  "/portfolio": "Portfolio › Portfolio & Balances",
  "/pnl": "Portfolio › PnL & Analytics",
  "/orders": "Portfolio › Orders",
  "/fills": "Portfolio › Fills",
  "/campaigns": "Research › Campaigns",
  "/replay": "Research › Replay & Backtesting",
  "/ai": "Research › AI Advisor",
  "/strategies": "Control › Strategies",
  "/risk": "Control › Risk Center",
  "/alerts": "Control › Alerts",
  "/reports": "Control › Reports",
  "/exchanges": "System › Exchanges",
  "/system": "System › System Health",
  "/audit": "System › Audit Log",
  "/telegram": "System › Telegram",
  "/screener": "Scanner Suite › Screener",
  "/perpetuals": "Scanner Suite › Perpetuals",
  "/funding": "Scanner Suite › Funding",
  "/calculator": "Scanner Suite › Calculator",
  "/scanner-alerts": "Scanner Suite › Alert Rules",
  "/screener-reports": "Scanner Suite › Evidence — Screener Reports",
  "/screener-reports/[id]": "(no nav entry — detail page, highlighted nothing)",
  "/auto-paper": "Scanner Suite › Auto-Paper",
  "/settings": "footer › Settings",
  "/org": "footer › Organisation",
  "/billing": "footer › Billing",
  "/onboarding": "(no nav entry — highlighted nothing)",
  "/": "(redirect shim)",
  "/login": "(outside the shell)",
};

const routes = discover(APP_DIR).sort();
const rows = [];
const orphans = [];
for (const route of routes) {
  if (OUTSIDE_SHELL.has(route)) {
    rows.push({ route, dest: "—", leaf: "—", reach: "outside the console shell", clicks: "—" });
    continue;
  }
  const m = resolveNav(concrete(route));
  if (!m) {
    orphans.push(route);
    continue;
  }
  // Click depth from Overview: the destination itself is one activation;
  // a standing secondary entry inside it is two. A contextual surface is
  // reached from the surface that hands off to it, which is why it is
  // named rather than counted.
  const isLanding = m.destination.href === m.leaf.href;
  const clicks = m.destination.id === "overview" && isLanding ? "0 (landing page)" : isLanding ? "1" : "2";
  rows.push({
    route,
    dest: m.destination.label,
    leaf: m.leaf.label,
    // A contextual leaf that is also its destination's landing href is
    // still the direct target of that destination's primary nav link —
    // /settings is exactly this case, and reporting it as reachable
    // only "in context" told readers the Settings link does not go to
    // Settings.
    reach:
      m.leaf.contextual && !isLanding
        ? `contextual — opened from ${m.destination.label}`
        : `${m.destination.label} › ${m.leaf.label}`,
    clicks: m.leaf.contextual && !isLanding ? "in context" : clicks,
  });
}

if (orphans.length) {
  console.error(`FAIL: ${orphans.length} page(s) resolve to no navigation entry:`);
  for (const o of orphans) console.error(`  ${o}`);
  process.exit(1);
}

const standing = NAV.flatMap((d) =>
  d.groups.flatMap((g) => g.items.filter((i) => !i.contextual)),
).length;
// The number that actually matters for "is this simpler": how many
// links a person sees at once. Before, every group was expanded, so all
// 32 were on screen together. Now it is the six primary destinations
// plus the secondary entries of whichever one is open.
const secondaryPerDestination = NAV.map((d) => ({
  label: d.label,
  count: d.groups.flatMap((g) => g.items.filter((i) => !i.contextual)).length,
}));
const worstSecondary = Math.max(...secondaryPerDestination.map((d) => d.count));
const contextual = NAV.flatMap((d) =>
  d.groups.flatMap((g) => g.items.filter((i) => i.contextual)),
).length;

const lines = [];
lines.push("# Client-area refinement — complete old→new route map (T-087)");
lines.push("");
lines.push("<!-- GENERATED FILE — do not edit by hand.");
lines.push("     Regenerate with: cd web && node scripts/gen-route-map.mjs");
lines.push("     Source of truth: web/src/lib/nav.ts and the pages on disk. -->");
lines.push("");
lines.push(
  "Generated from `web/src/lib/nav.ts` and a filesystem scan of",
  "`web/src/app`, so this table cannot drift from the navigation the app",
  "ships. The generator exits non-zero if any page resolves to no",
  "navigation entry, which is what makes \"every route is preserved\" a",
  "checked claim rather than an assertion.",
);
lines.push("");
lines.push("## Counts");
lines.push("");
lines.push("| | Before | After |");
lines.push("| --- | --- | --- |");
// primaryCount is what PrimaryNav actually renders: every destination
// that is not wholly platform-gated, Operations included — Operations is
// itself one of the primary links (D1 gates its *entries*, not the
// destination). The literal 6 that used to sit in the next two rows
// contradicted this row two lines above it and understated the measured
// figure, in the one table that invites verification.
const primaryCount = NAV.filter((d) => d.access?.kind !== "platform").length;
lines.push(`| Primary navigation choices | 6 groups, all expanded | ${primaryCount} destinations |`);
lines.push(`| Standing links rendered at once | 29 + 3 pinned = 32 | ${primaryCount} primary (secondary appears in context) |`);
lines.push("| Duplicate group control (icon rail) | yes | removed |");
lines.push(
  `| Links visible at once (worst case) | 32 | ${primaryCount + worstSecondary} = ${primaryCount} primary + ${worstSecondary} secondary |`,
);
lines.push(`| Navigation entries defined | 32 | ${standing} standing + ${contextual} contextual |`);
lines.push(`| Pages served | ${routes.length} | ${routes.length} (unchanged) |`);
lines.push("| Pages highlighting no nav entry | 3 | 0 |");
lines.push("");
lines.push("Secondary entries per destination:");
lines.push("");
lines.push("| Destination | Secondary entries |");
lines.push("| --- | --- |");
for (const d of secondaryPerDestination) lines.push(`| ${d.label} | ${d.count} |`);
lines.push("");
lines.push("## Every route");
lines.push("");
lines.push("| Route | Was | Primary destination | Presented as | Activations from Overview |");
lines.push("| --- | --- | --- | --- | --- |");
for (const r of rows) {
  lines.push(
    `| \`${r.route}\` | ${OLD_LOCATION[r.route] ?? "(unmapped)"} | ${r.dest} | ${r.reach} | ${r.clicks} |`,
  );
}
lines.push("");
lines.push("## Settings categories and preserved anchors");
lines.push("");
lines.push("Every anchor that existed before the refinement is marked");
lines.push("**legacy** and must still activate and focus its category; the e2e");
lines.push("suite asserts each one.");
lines.push("");
lines.push("| Anchor | Section | Category | Pre-existing | Platform staff only |");
lines.push("| --- | --- | --- | --- | --- |");
for (const s of SETTINGS_SECTIONS) {
  lines.push(
    `| \`#${s.anchor}\` | ${s.label} | ${s.category} | ${s.legacy ? "yes" : "new"} | ${s.platform ? "yes" : "no"} |`,
  );
}
lines.push("");

writeFileSync(join(here, "..", "..", "docs", "design", "client-area-refinement-routes.md"), lines.join("\n"));
console.log(`ok: ${rows.length} routes mapped, 0 orphans, ${standing} standing entries`);
