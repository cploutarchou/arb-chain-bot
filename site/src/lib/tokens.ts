import { siteConfig, PLACEHOLDERS } from "../../site.config";

// {{token}} resolution for content (docs/site/**/*.md) and for JSX.
// Unset values get the placeholder; in development the placeholder carries
// a visible "unset" marker so a page with a placeholder is obvious in the
// browser and never mistaken for finished copy.

const DEV = process.env.NODE_ENV !== "production";

function mark(value: string, name: string, html: boolean): string {
  if (!DEV) return value;
  return html
    ? `${value}<span class="unset" title="site.config.ts: ${name} is unset">[unset: ${name}]</span>`
    : `${value} [unset: ${name}]`;
}

export function resolveToken(name: string, html = true): string {
  if (name === "brand") {
    return siteConfig.brand || mark(PLACEHOLDERS.brand, "brand", html);
  }
  if (name === "company") {
    return siteConfig.company || mark(PLACEHOLDERS.company, "company", html);
  }
  if (name === "site_url") {
    return siteConfig.siteUrl || mark(PLACEHOLDERS.site_url, "site_url", html);
  }
  if (name.startsWith("price.")) {
    const key = name.slice("price.".length) as keyof typeof siteConfig.prices;
    return siteConfig.prices[key] || mark(PLACEHOLDERS.price, name, html);
  }
  if (name.startsWith("aff.")) {
    const key = name.slice("aff.".length) as keyof typeof siteConfig.aff;
    return siteConfig.aff[key] || mark(PLACEHOLDERS.aff, name, html);
  }
  // Unknown token: leave it visible rather than silently dropping text.
  return `{{${name}}}`;
}

export function resolveTokens(text: string, html = true): string {
  return text.replace(/\{\{\s*([a-z_.]+)\s*\}\}/gi, (_, n: string) =>
    resolveToken(n, html),
  );
}

/** Plain-text brand for <title>, meta and JSX text. */
export const brand = resolveToken("brand", false);
export const company = resolveToken("company", false);
