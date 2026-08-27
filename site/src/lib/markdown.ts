import { marked } from "marked";

// Build-time markdown → HTML for our own content only (docs/site,
// docs/user-guide). Never used on user input, so raw HTML is passed through
// (the drafts use none) and no sanitiser is needed. HTML comments are
// author notes and are removed before rendering.

marked.use({ gfm: true, breaks: false });

// Wrap tables so wide ones scroll inside their own container.
function wrapTables(html: string): string {
  return html.replace(/<table>[\s\S]*?<\/table>/g, (t) => `<div class="table-wrap">${t}</div>`);
}

export function stripComments(md: string): string {
  return md.replace(/<!--[\s\S]*?-->/g, "");
}

/** Rewrites `[COUNSEL: …]` drafting notes into a visibly marked span. */
function markCounsel(html: string): string {
  return html.replace(
    /\[COUNSEL:?([^\]]*)\]/g,
    (_, body: string) =>
      `<span class="counsel">[Counsel note:${body}]</span>`,
  );
}

/** Rewrites relative `foo.md` links (user guide) to `/docs/foo`. */
function rewriteDocLinks(md: string): string {
  return md.replace(
    /\]\((?!https?:|\/|#)([a-z0-9-]+)\.md(#[^)]*)?\)/gi,
    (_, slug: string, hash: string | undefined) =>
      `](/docs/${slug === "README" ? "" : slug}${hash ?? ""})`,
  );
}

export function renderMarkdown(md: string): string {
  const html = marked.parse(rewriteDocLinks(stripComments(md)), {
    async: false,
  }) as string;
  return wrapTables(markCounsel(html));
}

/** Inline-only rendering (no <p> wrapper) for single-line fields. */
export function renderInline(md: string): string {
  return markCounsel(marked.parseInline(stripComments(md), { async: false }) as string);
}
