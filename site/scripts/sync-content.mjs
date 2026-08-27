#!/usr/bin/env node
// sync-content.mjs — copies the markdown sources the site renders from
// ../docs into site/.content at build time. A copy, never a symlink: the
// static export must not depend on the monorepo layout at serve time, and
// the copy-lint runs against exactly the bytes that will be rendered.
//
// Sources:
//   docs/site/copy/*.md    -> .content/copy/
//   docs/site/legal/*.md   -> .content/legal/
//   docs/user-guide/*.md   -> .content/user-guide/
//   docs/campaigns/**/*.md -> .content/campaigns/  (evidence citations)

import { cpSync, mkdirSync, rmSync, existsSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..");
const docs = join(root, "..", "docs");
const out = join(root, ".content");

const MAP = [
  ["site/copy", "copy"],
  ["site/legal", "legal"],
  ["user-guide", "user-guide"],
  ["campaigns", "campaigns"],
];

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });

let files = 0;
for (const [src, dst] of MAP) {
  const from = join(docs, src);
  if (!existsSync(from)) {
    console.error(`sync-content: missing source ${from}`);
    process.exit(1);
  }
  cpSync(from, join(out, dst), {
    recursive: true,
    filter: (p) => p === from || /\.(md|json)$/.test(p) || !p.includes("."),
  });
  files += readdirSync(join(out, dst), { recursive: true }).length;
}
console.log(`sync-content: copied ${files} entries into ${out}`);
