import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { renderInline, renderMarkdown, stripComments } from "./markdown";
import { resolveTokens } from "./tokens";

// Content loader. Reads the build-time copy under site/.content (written by
// scripts/sync-content.mjs); falls back to ../docs so `next dev` works
// straight after a checkout. Always a file read, never a symlink.

const CONTENT_ROOT = existsSync(join(process.cwd(), ".content"))
  ? join(process.cwd(), ".content")
  : join(process.cwd(), "..", "docs");

const DIRS = {
  copy: existsSync(join(CONTENT_ROOT, "copy")) ? "copy" : "site/copy",
  legal: existsSync(join(CONTENT_ROOT, "legal")) ? "legal" : "site/legal",
  guide: "user-guide",
} as const;

export type Frontmatter = Record<string, string>;

/** Minimal frontmatter parser: `key: value` lines between `---` fences. */
export function parseFrontmatter(raw: string): {
  data: Frontmatter;
  body: string;
} {
  const m = raw.match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n?/);
  if (!m) return { data: {}, body: raw };
  const data: Frontmatter = {};
  for (const line of (m[1] ?? "").split(/\r?\n/)) {
    const kv = line.match(/^([A-Za-z0-9_-]+):\s*(.*)$/);
    if (kv) data[kv[1] as string] = (kv[2] ?? "").trim();
  }
  return { data, body: raw.slice(m[0].length) };
}

export interface Section {
  /** Heading text without the `## `. */
  title: string;
  /** Raw markdown body of the section (tokens resolved). */
  md: string;
  /** Rendered HTML of the body. */
  html: string;
  /** `**Key:** value` fields found in the section, keyed lower-case. */
  fields: Record<string, string>;
  /** `### ` sub-sections, in order. */
  subsections: Section[];
}

function parseFields(md: string): Record<string, string> {
  const out: Record<string, string> = {};
  const re = /^\*\*([^*]+?):\*\*\s*([\s\S]*?)(?=\n\s*\n|\n\*\*[^*]+?:\*\*|$)/gm;
  let m: RegExpExecArray | null;
  while ((m = re.exec(md))) {
    const key = (m[1] ?? "").trim().toLowerCase();
    out[key] = renderInline((m[2] ?? "").replace(/\s*\n\s*/g, " ").trim());
  }
  return out;
}

function splitSections(md: string, level: 2 | 3): Section[] {
  const hashes = "#".repeat(level);
  const re = new RegExp(`^${hashes} (?!#)(.+)$`, "m");
  const parts = md.split(new RegExp(`^(?=${hashes} (?!#))`, "m"));
  const out: Section[] = [];
  for (const part of parts) {
    const hm = part.match(re);
    if (!hm) continue;
    const title = (hm[1] ?? "").trim();
    const body = part.slice(hm[0].length).trim();
    out.push({
      title,
      md: body,
      html: renderMarkdown(body),
      fields: parseFields(body),
      subsections: level === 2 ? splitSections(body, 3) : [],
    });
  }
  return out;
}

export interface CopyPage {
  data: Frontmatter;
  /** Body markdown with tokens resolved and comments stripped. */
  body: string;
  sections: Section[];
  /** Everything after the final `---` rule (the risk summary / footer). */
  tail: string;
  tailHtml: string;
  /** Text before the first `##`, rendered (about.md uses an H1 line). */
  preambleHtml: string;
}

function readContent(dir: string, file: string): string {
  return readFileSync(join(CONTENT_ROOT, dir, file), "utf8");
}

export function loadCopy(name: string): CopyPage {
  const raw = readContent(DIRS.copy, `${name}.md`);
  const { data, body: rawBody } = parseFrontmatter(raw);
  const body = resolveTokens(stripComments(rawBody));
  // The trailing footer/risk block is separated by the last `\n---\n`.
  const cut = body.lastIndexOf("\n---\n");
  const main = cut >= 0 ? body.slice(0, cut) : body;
  const tail = cut >= 0 ? body.slice(cut + 5).trim() : "";
  const firstH2 = main.search(/^## /m);
  const preamble = firstH2 > 0 ? main.slice(0, firstH2) : firstH2 < 0 ? main : "";
  return {
    data,
    body,
    sections: splitSections(main, 2),
    tail,
    tailHtml: tail ? renderMarkdown(tail) : "",
    preambleHtml: preamble.trim() ? renderMarkdown(preamble) : "",
  };
}

export interface Doc {
  slug: string;
  title: string;
  data: Frontmatter;
  html: string;
  /** First paragraph, plain text, for meta description. */
  summary: string;
}

function toDoc(slug: string, raw: string): Doc {
  const { data, body } = parseFrontmatter(raw);
  const md = resolveTokens(stripComments(body));
  const h1 = md.match(/^# (.+)$/m);
  const title = data.title ?? (h1 ? (h1[1] ?? "").trim() : slug);
  // Drop the H1 when the page renders its own heading.
  const withoutH1 = h1 ? md.replace(h1[0], "") : md;
  const para = withoutH1
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .find((p) => p && !/^[#>|\-*]/.test(p) && !/^\*\*/.test(p));
  const summary = (para ?? "")
    .replace(/[*_`>\[\]]/g, "")
    .replace(/\([^)]*\)/g, "")
    .replace(/\s+/g, " ")
    .slice(0, 160);
  return { slug, title, data, html: renderMarkdown(withoutH1), summary };
}

export function loadLegal(slug: string): Doc {
  return toDoc(slug, readContent(DIRS.legal, `${slug}.md`));
}

export function listLegal(): string[] {
  return readdirSync(join(CONTENT_ROOT, DIRS.legal))
    .filter((f) => f.endsWith(".md"))
    .map((f) => f.replace(/\.md$/, ""))
    .sort();
}

export function loadGuide(slug: string): Doc {
  return toDoc(slug, readContent(DIRS.guide, `${slug}.md`));
}

export function listGuide(): string[] {
  return readdirSync(join(CONTENT_ROOT, DIRS.guide))
    .filter((f) => f.endsWith(".md") && f !== "README.md")
    .map((f) => f.replace(/\.md$/, ""))
    .sort();
}
