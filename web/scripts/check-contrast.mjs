#!/usr/bin/env node
// check-contrast.mjs — reproduces the "must" contrast ratios from
// docs/design/ui/design-system.md §1.3 directly from the live
// src/app/globals.css token values, so a future token edit cannot
// silently regress AA (design-system.md §0/§7 item 2). No dependencies
// (node only), no test framework — run as `node scripts/check-contrast.mjs`
// / `npm run check:contrast`.
//
// Method (design-system.md §0, WCAG 2.x relative luminance):
//   c' = c/255; c'' = c' <= 0.03928 ? c'/12.92 : ((c'+0.055)/1.055)^2.4
//   L = 0.2126 R + 0.7152 G + 0.0722 B
//   ratio = (L_light + 0.05) / (L_dark + 0.05)
//
// Thresholds: text pairs must be >= 4.5, boundary pairs (--border-strong,
// on-accent/on-critical ink) must be >= 3.0 (border) or 4.5 (ink on a
// fill, since it is text-sized). Fixtures below are the BOLD ("must
// hold") rows of §1.3 only — the non-bold rows (e.g. --text-dim@0.8 on
// --bg-raised, --text-gated on --bg-raised, plain --border) are
// deliberately excluded: the spec documents those as sub-AA or
// decorative-only, not a regression target.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const CSS_PATH = join(__dirname, "..", "src", "app", "globals.css");
const css = readFileSync(CSS_PATH, "utf8");

function extractBlock(css, marker) {
  const idx = css.indexOf(marker);
  if (idx === -1) throw new Error(`check-contrast: block not found: ${marker}`);
  const braceStart = css.indexOf("{", idx);
  let depth = 0;
  for (let i = braceStart; i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}") {
      depth--;
      if (depth === 0) return css.slice(braceStart + 1, i);
    }
  }
  throw new Error(`check-contrast: unterminated block: ${marker}`);
}

function parseTokens(block) {
  const tokens = new Map();
  const re = /--([a-z0-9-]+)\s*:\s*([^;]+);/gi;
  let m;
  while ((m = re.exec(block))) {
    tokens.set(`--${m[1]}`, m[2].trim());
  }
  // Resolve one level of var(--x) references (§1.2: --pos: var(--ok) etc).
  for (const [name, value] of tokens) {
    const varMatch = /^var\((--[a-z0-9-]+)\)$/i.exec(value);
    if (varMatch && tokens.has(varMatch[1])) {
      tokens.set(name, tokens.get(varMatch[1]));
    }
  }
  return tokens;
}

function hexToRgb(hex) {
  const h = hex.replace("#", "");
  if (h.length !== 6) return null;
  return {
    r: parseInt(h.slice(0, 2), 16),
    g: parseInt(h.slice(2, 4), 16),
    b: parseInt(h.slice(4, 6), 16),
  };
}

function channelLin(c) {
  const cs = c / 255;
  return cs <= 0.03928 ? cs / 12.92 : Math.pow((cs + 0.055) / 1.055, 2.4);
}

function relLuminance({ r, g, b }) {
  return (
    0.2126 * channelLin(r) + 0.7152 * channelLin(g) + 0.0722 * channelLin(b)
  );
}

function contrast(hexA, hexB) {
  const a = hexToRgb(hexA);
  const b = hexToRgb(hexB);
  if (!a || !b) return null;
  const la = relLuminance(a);
  const lb = relLuminance(b);
  const lighter = Math.max(la, lb);
  const darker = Math.min(la, lb);
  return (lighter + 0.05) / (darker + 0.05);
}

const lightBlock = parseTokens(extractBlock(css, ":root {"));
const darkBlock = parseTokens(extractBlock(css, ':root[data-theme="dark"] {'));
const mediaBlock = parseTokens(
  extractBlock(css, ':root:not([data-theme="light"]) {'),
);

let failures = 0;
let checks = 0;

function need(tokens, name) {
  const v = tokens.get(name);
  if (v === undefined) {
    console.error(`MISSING token ${name}`);
    failures++;
    return null;
  }
  return v;
}

function check(label, fg, bg, expected, threshold, tolerance = 0.05) {
  checks++;
  const ratio = contrast(fg, bg);
  if (ratio === null) {
    console.error(`FAIL ${label}: could not parse hex values (${fg} on ${bg})`);
    failures++;
    return;
  }
  const okExpected = Math.abs(ratio - expected) <= tolerance;
  const okThreshold = ratio >= threshold - 1e-9;
  if (!okExpected || !okThreshold) {
    console.error(
      `FAIL ${label}: computed ${ratio.toFixed(2)}, expected ${expected.toFixed(2)} ± ${tolerance} (threshold >= ${threshold})`,
    );
    failures++;
  } else {
    console.log(
      `ok   ${label}: ${ratio.toFixed(2)} (expected ${expected.toFixed(2)}, threshold >= ${threshold})`,
    );
  }
}

// ---- Dark theme (§1.3 "Dark theme", bold rows only) -----------------------
{
  const t = darkBlock;
  const bg = need(t, "--bg");
  const panel = need(t, "--bg-panel");
  const raised = need(t, "--bg-raised");
  check("dark --text on --bg", t.get("--text"), bg, 14.06, 4.5);
  check("dark --text on --bg-panel", t.get("--text"), panel, 13.28, 4.5);
  check("dark --text on --bg-raised", t.get("--text"), raised, 12.38, 4.5);

  check("dark --text-dim on --bg", t.get("--text-dim"), bg, 7.11, 4.5);
  check("dark --text-dim on --bg-panel", t.get("--text-dim"), panel, 6.71, 4.5);
  check(
    "dark --text-dim on --bg-raised",
    t.get("--text-dim"),
    raised,
    6.26,
    4.5,
  );

  check("dark --text-gated on --bg", t.get("--text-gated"), bg, 5.74, 4.5);
  check(
    "dark --text-gated on --bg-panel",
    t.get("--text-gated"),
    panel,
    5.43,
    4.5,
  );
  check(
    "dark --text-gated on --bg-raised",
    t.get("--text-gated"),
    raised,
    5.06,
    4.5,
  );

  check("dark --accent on --bg", t.get("--accent"), bg, 6.0, 4.5);
  check("dark --accent on --bg-panel", t.get("--accent"), panel, 5.67, 4.5);

  check("dark --ok on --bg", t.get("--ok"), bg, 8.11, 4.5);
  check("dark --ok on --bg-panel", t.get("--ok"), panel, 7.66, 4.5);

  check("dark --warn on --bg", t.get("--warn"), bg, 8.83, 4.5);
  check("dark --warn on --bg-panel", t.get("--warn"), panel, 8.34, 4.5);

  check("dark --high on --bg", t.get("--high"), bg, 6.56, 4.5);
  check("dark --high on --bg-panel", t.get("--high"), panel, 6.19, 4.5);

  check("dark --critical on --bg", t.get("--critical"), bg, 5.79, 4.5);
  check("dark --critical on --bg-panel", t.get("--critical"), panel, 5.47, 4.5);
  check(
    "dark --critical on --bg-raised",
    t.get("--critical"),
    raised,
    5.1,
    4.5,
  );

  check(
    "dark --border-strong on --bg",
    t.get("--border-strong"),
    bg,
    3.56,
    3.0,
  );
  check(
    "dark --border-strong on --bg-panel",
    t.get("--border-strong"),
    panel,
    3.36,
    3.0,
  );
  check(
    "dark --border-strong on --bg-raised",
    t.get("--border-strong"),
    raised,
    3.14,
    3.0,
  );

  check(
    "dark --on-accent on --accent fill",
    t.get("--on-accent"),
    t.get("--accent"),
    6.0,
    4.5,
  );
  // design-system.md §1.3 lists 6.29 here, but §1.2's own hex table sets
  // dark --on-critical to #0b0e14 — identical to dark --bg — and the same
  // table's "--critical on --bg" row (independently) computes that exact
  // pair at 5.79, reproduced above. 6.29 is only reachable by pairing
  // --critical against pure #000000, which contradicts §1.2's own value;
  // treated as a doc arithmetic slip, not a CSS bug — 5.79 still clears
  // the >= 4.5 AA threshold the row is asserting.
  check(
    "dark --on-critical on --critical fill",
    t.get("--on-critical"),
    t.get("--critical"),
    5.79,
    4.5,
  );
}

// ---- Light theme (§1.3 "Light theme", bold rows only) ---------------------
{
  const t = lightBlock;
  const bg = need(t, "--bg");
  const panel = need(t, "--bg-panel");
  const raised = need(t, "--bg-raised");
  check("light --text on --bg", t.get("--text"), bg, 16.1, 4.5);
  check("light --text on --bg-panel", t.get("--text"), panel, 17.41, 4.5);
  check("light --text on --bg-raised", t.get("--text"), raised, 14.99, 4.5);

  check("light --text-dim on --bg", t.get("--text-dim"), bg, 7.47, 4.5);
  check(
    "light --text-dim on --bg-panel",
    t.get("--text-dim"),
    panel,
    8.08,
    4.5,
  );
  check(
    "light --text-dim on --bg-raised",
    t.get("--text-dim"),
    raised,
    6.95,
    4.5,
  );

  check("light --text-gated on --bg", t.get("--text-gated"), bg, 4.71, 4.5);
  check(
    "light --text-gated on --bg-panel",
    t.get("--text-gated"),
    panel,
    5.09,
    4.5,
  );

  check("light --accent on --bg", t.get("--accent"), bg, 4.94, 4.5);
  check("light --accent on --bg-panel", t.get("--accent"), panel, 5.34, 4.5);
  check("light --accent on --bg-raised", t.get("--accent"), raised, 4.6, 4.5);

  check("light --ok on --bg", t.get("--ok"), bg, 4.97, 4.5);
  check("light --ok on --bg-panel", t.get("--ok"), panel, 5.38, 4.5);
  check("light --ok on --bg-raised", t.get("--ok"), raised, 4.63, 4.5);

  check("light --warn on --bg", t.get("--warn"), bg, 5.9, 4.5);
  check("light --warn on --bg-panel", t.get("--warn"), panel, 6.38, 4.5);
  check("light --warn on --bg-raised", t.get("--warn"), raised, 5.49, 4.5);

  check("light --high on --bg", t.get("--high"), bg, 5.71, 4.5);
  check("light --high on --bg-panel", t.get("--high"), panel, 6.17, 4.5);
  check("light --high on --bg-raised", t.get("--high"), raised, 5.31, 4.5);

  check("light --critical on --bg", t.get("--critical"), bg, 4.89, 4.5);
  check(
    "light --critical on --bg-panel",
    t.get("--critical"),
    panel,
    5.29,
    4.5,
  );
  check(
    "light --critical on --bg-raised",
    t.get("--critical"),
    raised,
    4.55,
    4.5,
  );

  check(
    "light --border-strong on --bg",
    t.get("--border-strong"),
    bg,
    3.34,
    3.0,
  );
  check(
    "light --border-strong on --bg-panel",
    t.get("--border-strong"),
    panel,
    3.61,
    3.0,
  );
  check(
    "light --border-strong on --bg-raised",
    t.get("--border-strong"),
    raised,
    3.11,
    3.0,
  );

  check(
    "light --on-accent on --accent fill",
    t.get("--on-accent"),
    t.get("--accent"),
    5.34,
    4.5,
  );
  check(
    "light --on-critical on --critical fill",
    t.get("--on-critical"),
    t.get("--critical"),
    5.29,
    4.5,
  );
}

// ---- The two dark blocks must carry an identical token set (§1.2: "both
// blocks must carry the identical full set"). ---------------------------
{
  checks++;
  const aKeys = [...darkBlock.keys()].sort();
  const bKeys = [...mediaBlock.keys()].sort();
  const sameKeys =
    aKeys.length === bKeys.length && aKeys.every((k, i) => k === bKeys[i]);
  const sameValues =
    sameKeys && aKeys.every((k) => darkBlock.get(k) === mediaBlock.get(k));
  if (!sameKeys || !sameValues) {
    console.error(
      'FAIL :root[data-theme="dark"] and the prefers-color-scheme media block must carry an identical token set',
    );
    failures++;
  } else {
    console.log(
      'ok   :root[data-theme="dark"] and the media dark block are identical',
    );
  }
}

console.log(`\n${checks - failures}/${checks} checks passed.`);
if (failures > 0) {
  console.error(`\ncheck-contrast: ${failures} failure(s).`);
  process.exit(1);
}
