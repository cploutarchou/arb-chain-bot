import { marked } from "marked";
import sanitizeHtml from "sanitize-html";

// Build-time markdown → HTML for our own content only (docs/site,
// docs/user-guide). The rendered HTML crosses into the page through
// dangerouslySetInnerHTML, so it is passed through an allow-list
// sanitiser before it gets there (audit S15): the drafts are first-party
// today, but a compromised content file must not become stored XSS on
// the marketing site. The allow-list is deliberately small — everything
// marked's own GFM output needs, and nothing else. HTML comments are
// author notes and are removed before rendering.

marked.use({ gfm: true, breaks: false });

const sanitizeOptions: sanitizeHtml.IOptions = {
  allowedTags: [
    "p", "br", "hr", "blockquote", "code", "pre", "em", "strong", "del",
    "s", "sup", "sub", "ul", "ol", "li", "table", "thead", "tbody", "tr",
    "th", "td", "a", "span", "div", "h1", "h2", "h3", "h4", "h5", "h6",
    "img", "input",
  ],
  allowedAttributes: {
    a: ["href", "title"],
    img: ["src", "alt", "title", "width", "height"],
    // GFM task-list checkboxes; disabled + readonly, never interactive.
    input: ["type", "checked", "disabled"],
    // wrapTables' scroll container and the counsel-note marker.
    div: ["class"],
    span: ["class"],
    th: ["style"], // marked's GFM emits text-align on th only
    td: ["style"],
  },
  allowedSchemes: ["http", "https", "mailto"],
  // text-align: left|center|right is all marked ever emits on th/td.
  allowedStyles: {
    th: { "text-align": [/^(left|center|right)$/] },
    td: { "text-align": [/^(left|center|right)$/] },
  },
  allowProtocolRelative: false,
  transformTags: {
    input: (tagName, attribs) => ({
      tagName,
      attribs: { ...attribs, disabled: "disabled", readonly: "readonly" },
    }),
  },
};

/** Allow-list sanitiser every rendered string passes through (S15). */
export function sanitize(html: string): string {
  return sanitizeHtml(html, sanitizeOptions);
}

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
  return sanitize(wrapTables(markCounsel(html)));
}

/** Inline-only rendering (no <p> wrapper) for single-line fields. */
export function renderInline(md: string): string {
  return sanitize(markCounsel(marked.parseInline(stripComments(md), { async: false }) as string));
}
