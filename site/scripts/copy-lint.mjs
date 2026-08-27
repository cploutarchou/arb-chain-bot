#!/usr/bin/env node
// copy-lint.mjs — the build-time copy gate required by
// docs/compliance/review-2026-08-27.md item 4 (every %/currency figure must
// carry a docs/campaigns/<id> citation) and item 6 (banned words). Runs
// before `next build` (package.json "build") and in CI (site.yml). Exit 1
// on any finding; the finding list is the whole output.
//
// Scope — what is "page content":
//   .content/copy/*.md     every file with a `slug:` in its frontmatter
//                          (rendered as a marketing page)
//   .content/legal/*.md    every file (rendered under /legal)
//   src/**/*.ts, *.tsx     page source, including packages.ts and any
//                          string a component renders
//   .content/user-guide/*  rendered under /docs. Banned words are checked.
//                          The figure rule is NOT applied there: the guide
//                          documents model inputs and worked examples
//                          ("Numbers in this guide are model inputs or
//                          worked examples, never results" — README) and is
//                          not a marketing surface. Nothing in that folder is
//                          edited by this package; if the user guide ever
//                          starts quoting results, extend PAGE_FIGURE_SCOPE.
//
// Rule 1 — figures. A "figure" is a number with a percentage or currency
// unit: `12 %`, `0.5%`, `5 bps`, `10 percent`, `$39`, `€25`, `£1`, `0 USDT`,
// `2,190 USD`, `EUR 9`. A figure is allowed only inside a block that cites
// `docs/campaigns/<id>`. A block is a blockquote (contiguous `>` lines), a
// single list item, or a paragraph (lines separated by a blank line). HTML comments are stripped
// first: they are author notes, not page content, and the renderer removes
// them.
//
// Rule 2 — banned words: guaranteed, risk-free, passive income, harvest
// (the identifier `funding_harvest` is exempt; it is a strategy id in the
// backend). Word-boundary, case-insensitive.
//
// Allow-list — exactly one: docs/site/legal/affiliate-terms.md §5 item 4
// ("Performance claims"), which forbids affiliates from using those words
// and must therefore name them. The allow-list is keyed on the file AND the
// list item text, so moving or rewording the clause revokes it.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..");
const content = join(root, ".content");

const CAMPAIGN_CITE = /docs\/campaigns\/[A-Za-z0-9_\/-]+/;

// Percent / bps / currency figures. Currency codes are the ones the product
// quotes in; symbols cover the three markets the legal drafts address.
const FIGURE_PATTERNS = [
  /\b\d[\d,.]*\s?(%|percent\b|per cent\b|bps\b)/i,
  /(?<![\w$])[$€£]\s?\d/,
  /\b\d[\d,.]*\s?(USDT|USDC|USD|EUR|GBP)\b/,
  /\b(USDT|USDC|USD|EUR|GBP)\s?\d/,
];

const BANNED = [
  { word: "guaranteed", re: /\bguaranteed\b/i },
  { word: "risk-free", re: /\brisk[- ]free\b/i },
  { word: "passive income", re: /\bpassive\s+income\b/i },
  // `harvest` but not the identifier funding_harvest.
  { word: "harvest", re: /(?<!funding_)\bharvest(ing|ed|s)?\b/i },
];

const ALLOW = [
  {
    file: "legal/affiliate-terms.md",
    // §5 item 4, the "Performance claims" clause.
    blockMatch: /^4\.\s+\*\*Performance claims\.\*\*/,
    words: ["guaranteed", "risk-free", "passive income"],
  },
];

function walk(dir, exts, acc = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) walk(p, exts, acc);
    else if (exts.some((e) => p.endsWith(e))) acc.push(p);
  }
  return acc;
}

function stripComments(text) {
  return text.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, " "));
}

function frontmatterHasSlug(text) {
  const m = text.match(/^---\n([\s\S]*?)\n---/);
  return Boolean(m && /^slug:\s*\S/m.test(m[1]));
}

// Split into blocks keeping the 1-based start line of each block.
function blocks(text) {
  const lines = text.split("\n");
  const out = [];
  let cur = null;
  const flush = () => {
    if (cur) out.push(cur);
    cur = null;
  };
  lines.forEach((line, i) => {
    const isQuote = /^\s*>/.test(line);
    if (line.trim() === "") {
      flush();
      return;
    }
    // A new list item starts a new block, so a citation in one item never
    // covers a figure in the next.
    const isItem = /^\s*(\d+\.|[-*])\s/.test(line);
    if (cur && (cur.quote !== isQuote || isItem)) flush();
    if (!cur) cur = { start: i + 1, quote: isQuote, lines: [] };
    cur.lines.push(line);
  });
  flush();
  return out.map((b) => ({ ...b, text: b.lines.join("\n") }));
}

const findings = [];

function lintFile(path, { figures, allow }) {
  const rel = relative(root, path);
  const raw = readFileSync(path, "utf8");
  const text = stripComments(raw);
  for (const b of blocks(text)) {
    const cited = CAMPAIGN_CITE.test(b.text);
    const allowed = new Set(
      allow
        .filter((a) => a.blockMatch.test(b.text.trim()))
        .flatMap((a) => a.words),
    );
    b.lines.forEach((line, j) => {
      const ln = b.start + j;
      if (figures && !cited) {
        for (const re of FIGURE_PATTERNS) {
          const m = line.match(re);
          if (m) {
            findings.push(
              `${rel}:${ln}: figure "${m[0].trim()}" outside a block citing docs/campaigns/<id>`,
            );
            break;
          }
        }
      }
      for (const { word, re } of BANNED) {
        if (allowed.has(word)) continue;
        const m = line.match(re);
        if (m) findings.push(`${rel}:${ln}: banned word "${m[0]}"`);
      }
    });
  }
}

let checked = 0;

for (const f of walk(join(content, "copy"), [".md"])) {
  if (!frontmatterHasSlug(readFileSync(f, "utf8"))) continue;
  lintFile(f, { figures: true, allow: [] });
  checked++;
}
for (const f of walk(join(content, "legal"), [".md"])) {
  const rel = relative(content, f).replace(/\\/g, "/");
  lintFile(f, { figures: true, allow: ALLOW.filter((a) => a.file === rel) });
  checked++;
}
for (const f of walk(join(root, "src"), [".ts", ".tsx"])) {
  lintFile(f, { figures: true, allow: [] });
  checked++;
}
for (const f of walk(join(content, "user-guide"), [".md"])) {
  lintFile(f, { figures: false, allow: [] });
  checked++;
}

if (findings.length) {
  console.error(`copy-lint: ${findings.length} finding(s) in ${checked} files`);
  for (const f of findings) console.error("  " + f);
  process.exit(1);
}
console.log(`copy-lint: ok (${checked} files, 0 findings)`);
