import { loadCopy } from "@/lib/content";
import { pageMeta } from "@/lib/meta";
import { resolveToken } from "@/lib/tokens";
import { CAPABILITY_ROWS, LIVE_EXECUTION_ROW, PACKAGES } from "@/lib/packages";
import { Container, CopySection, CtaButton, Evidence, Hero, Tail } from "@/components/Page";

export const metadata = pageMeta(
  "Pricing",
  "Five packages, one product. Capability and limit table; no package places orders.",
  "/pricing",
);

const PRICE_KEYS = {
  Watch: { monthly: "price.watch.monthly", annual: null },
  Signal: { monthly: "price.signal.monthly", annual: "price.signal.annual" },
  Operator: { monthly: "price.operator.monthly", annual: "price.operator.annual" },
  Desk: { monthly: "price.desk.monthly", annual: "price.desk.annual" },
  Institution: { monthly: null, annual: "price.institution.annual" },
} as const;

function PriceCell({ token }: { token: string | null }) {
  // Price strings come from site.config.ts (Paddle previews at
  // publication). Unset -> "—" placeholder, marked "unset" in dev.
  return <span dangerouslySetInnerHTML={{ __html: token ? resolveToken(token) : "—" }} />;
}

export default function Pricing() {
  const page = loadCopy("pricing");
  const by = (t: string) => page.sections.find((s) => s.title === t);
  const hero = by("Hero");
  const cards = by("Package cards");
  const includes = by("What every package includes");
  const evidence = by("Evidence");
  const faq = by("Billing FAQ");

  return (
    <>
      {hero && <Hero section={hero} />}

      {cards && (
        <section className="py-12 md:py-16">
          <Container>
            <h2 className="t-h2">Packages</h2>
            <p className="t-small mt-2 text-[var(--text-dim)]" dangerouslySetInnerHTML={{ __html: hero?.fields["toggle"] ?? "" }} />
            <div className="mt-8 grid gap-6 md:grid-cols-2 lg:grid-cols-3">
              {cards.subsections.map((s) => {
                const recommended = s.title.startsWith("Operator");
                // First line of the card body is the price token line; the
                // loader has already resolved it. Strip it from the body.
                const [priceLine, ...restLines] = s.md.split("\n");
                const cta = s.fields["cta"];
                const bodyMd = restLines.join("\n").replace(/\*\*CTA:\*\*.*$/m, "");
                return (
                  <div
                    key={s.title}
                    className={`flex flex-col rounded border bg-[var(--bg-panel)] p-5 ${recommended ? "border-t-[3px] border-t-[var(--accent)] border-[var(--border)]" : "border-[var(--border)]"}`}
                  >
                    <h3 className="t-h3">{s.title}</h3>
                    <p className="mt-2 text-[var(--text)] font-semibold" dangerouslySetInnerHTML={{ __html: priceLine ?? "" }} />
                    <PackageBody md={bodyMd} />
                    {cta && (
                      <div className="mt-auto pt-4">
                        <CtaButton html={cta} secondary={!recommended} />
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </Container>
        </section>
      )}

      <section className="py-12 md:py-16">
        <Container>
          <h2 className="t-h2">Comparison table</h2>
          <p className="t-small mt-2 text-[var(--text-dim)]">
            Capabilities and limits only. Limits are proposals until the
            operator confirms them. Prices are shown as localised previews
            from the payment processor at checkout.
          </p>
          <div className="mt-6 overflow-x-auto">
            <table className="w-full min-w-[820px] border-collapse t-small">
              <thead>
                <tr>
                  <th className="border border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 text-left text-[var(--text-dim)]">
                    Capability
                  </th>
                  {PACKAGES.map((p) => (
                    <th key={p} className="border border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 text-left">
                      {p}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                <tr>
                  <th className="border border-[var(--border)] px-3 py-2 text-left font-medium">Monthly</th>
                  {PACKAGES.map((p) => (
                    <td key={p} className="border border-[var(--border)] px-3 py-2">
                      <PriceCell token={PRICE_KEYS[p].monthly} />
                    </td>
                  ))}
                </tr>
                <tr>
                  <th className="border border-[var(--border)] px-3 py-2 text-left font-medium">Annual</th>
                  {PACKAGES.map((p) => (
                    <td key={p} className="border border-[var(--border)] px-3 py-2">
                      <PriceCell token={PRICE_KEYS[p].annual} />
                    </td>
                  ))}
                </tr>
                {CAPABILITY_ROWS.map((row) => (
                  <tr key={row.label}>
                    <th className="border border-[var(--border)] px-3 py-2 text-left font-medium">{row.label}</th>
                    {row.cells.map((c, i) => (
                      <td key={i} className={`border border-[var(--border)] px-3 py-2 ${c === "—" || c === "no" ? "text-[var(--text-dim)]" : ""}`}>
                        {c}
                      </td>
                    ))}
                  </tr>
                ))}
                <tr>
                  <th className="border border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 text-left font-semibold">
                    Live execution
                  </th>
                  {PACKAGES.map((p) => (
                    <td key={p} className="border border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 font-semibold">
                      {LIVE_EXECUTION_ROW}
                    </td>
                  ))}
                </tr>
              </tbody>
            </table>
          </div>
        </Container>
      </section>

      {includes && <CopySection section={includes} />}
      {evidence && <Evidence section={evidence} />}
      {faq && <CopySection section={faq} />}
      <Tail html={page.tailHtml} />
    </>
  );
}

function PackageBody({ md }: { md: string }) {
  // Lines: an intro paragraph then a `- ` list. Render the list with the
  // check glyph rows the style guide asks for.
  const lines = md.split("\n").map((l) => l.trim()).filter(Boolean);
  const intro = lines.filter((l) => !l.startsWith("- ")).join(" ");
  const items = lines.filter((l) => l.startsWith("- ")).map((l) => l.slice(2));
  return (
    <>
      {intro && <p className="t-small mt-2 text-[var(--text-dim)]">{intro}</p>}
      <ul className="mt-4 space-y-1.5 t-small">
        {items.map((it) => (
          <li key={it} className="flex gap-2">
            <svg aria-hidden="true" viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" className="mt-0.5 shrink-0 text-[var(--ok)]">
              <polyline points="3,8.5 6.5,12 13,4.5" />
            </svg>
            <span>{it}</span>
          </li>
        ))}
      </ul>
    </>
  );
}
