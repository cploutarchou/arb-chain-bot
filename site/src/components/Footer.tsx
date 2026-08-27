import Link from "next/link";
import { loadCopy } from "@/lib/content";
import { brand, company } from "@/lib/tokens";
import { CookieSettingsLink } from "./Consent";

const LEGAL = [
  ["terms", "Terms"],
  ["privacy", "Privacy"],
  ["risk-disclosure", "Risk Disclosure"],
  ["refund", "Refunds"],
  ["cookies", "Cookies"],
  ["affiliate-terms", "Affiliate Terms"],
  ["acceptable-use", "Acceptable Use"],
  ["dpa", "DPA"],
  ["subprocessors", "Subprocessors"],
] as const;

export function Footer() {
  // The site-wide risk line is the "Footer risk line" section of home.md,
  // so the footer and the copy draft can never drift apart.
  const home = loadCopy("home");
  const risk = home.sections.find((s) => s.title.startsWith("Footer risk line"));
  return (
    <footer className="mt-24 border-t border-[var(--border)] bg-[var(--bg-panel)]">
      <div className="mx-auto max-w-[1120px] px-4 py-10 md:px-6">
        {risk && (
          <div
            className="prose t-small max-w-none text-[var(--text-dim)]"
            dangerouslySetInnerHTML={{ __html: risk.html }}
          />
        )}
        <div className="mt-8 grid gap-8 md:grid-cols-3">
          <div>
            <div className="t-small font-semibold">Product</div>
            <ul className="mt-2 space-y-1 t-small text-[var(--text-dim)]">
              <li><Link href="/products/screener">Screener</Link></li>
              <li><Link href="/products/perpetuals">Perpetuals monitor</Link></li>
              <li><Link href="/products/triangular">Triangular engine</Link></li>
              <li><Link href="/products/auto-paper">Automatic paper execution</Link></li>
              <li><Link href="/pricing">Pricing</Link></li>
              <li><Link href="/docs">Documentation</Link></li>
            </ul>
          </div>
          <div>
            <div className="t-small font-semibold">Company</div>
            <ul className="mt-2 space-y-1 t-small text-[var(--text-dim)]">
              <li><Link href="/about">About</Link></li>
              <li><Link href="/faq">FAQ</Link></li>
              <li><Link href="/affiliates">Affiliates</Link></li>
            </ul>
          </div>
          <div>
            <div className="t-small font-semibold">Legal</div>
            <ul className="mt-2 space-y-1 t-small text-[var(--text-dim)]">
              {LEGAL.map(([slug, label]) => (
                <li key={slug}>
                  <Link href={`/legal/${slug}`}>{label}</Link>
                </li>
              ))}
              <li><CookieSettingsLink /></li>
            </ul>
          </div>
        </div>
        <p className="mt-8 t-caption">
          {brand} is operated by {company}. Legal pages on this site are
          drafts pending legal review. Nothing here is investment advice.
        </p>
      </div>
    </footer>
  );
}
