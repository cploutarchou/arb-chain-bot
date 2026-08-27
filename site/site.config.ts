// Site configuration — the single place where brand, company, prices and
// affiliate figures are set. Every `{{token}}` in docs/site/**/*.md resolves
// from here (src/lib/tokens.ts). Empty strings mean "not set yet": the
// loader substitutes the placeholder and, in development, appends a
// visible "unset" marker so nobody ships a page with a placeholder by
// accident.
//
// Prices are deliberately empty: packages.md §4 says prices are rendered
// from Paddle previews, never typed into copy. Until the Paddle catalog
// exists the price cells show the placeholder "—".

export type PriceKey =
  | "watch.monthly"
  | "signal.monthly"
  | "signal.annual"
  | "operator.monthly"
  | "operator.annual"
  | "desk.monthly"
  | "desk.annual"
  | "institution.annual";

export type AffKey =
  | "rate_year1"
  | "rate_after"
  | "rate_institution"
  | "threshold";

export interface SiteConfig {
  /** Product name; token {{brand}}. Placeholder "Brand". */
  brand: string;
  /** Legal entity; token {{company}}. Placeholder "Company Ltd". */
  company: string;
  /** Canonical origin used for metadata, sitemap and {{site_url}}. */
  siteUrl: string;
  /** Console origin the CTAs point at (sign-up, trial). */
  consoleUrl: string;
  /** {{price.<package>.<period>}} — display strings from Paddle previews. */
  prices: Partial<Record<PriceKey, string>>;
  /** {{aff.<key>}} — affiliate programme figures, set at publication. */
  aff: Partial<Record<AffKey, string>>;
  /** Default locale; the content loader is keyed by it for a later i18n pass. */
  locale: string;
}

export const siteConfig: SiteConfig = {
  brand: "",
  company: "",
  siteUrl: "https://example.invalid",
  consoleUrl: "https://console.example.invalid",
  prices: {},
  aff: {},
  locale: "en",
};

export const PLACEHOLDERS = {
  brand: "Brand",
  company: "Company Ltd",
  price: "—",
  aff: "—",
  site_url: "https://example.invalid",
} as const;
