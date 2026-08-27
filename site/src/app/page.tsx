import Link from "next/link";
import { loadCopy } from "@/lib/content";
import { pageMeta } from "@/lib/meta";
import { Container, CopySection, CtaButton, Evidence, Hero } from "@/components/Page";
import { Icon, type IconName } from "@/components/Icons";

export const metadata = pageMeta(
  "Home",
  "Measure the spread, then find out whether it was real. Alerts plus automatic paper execution, scored net of modelled fees.",
  "/",
);

const PRODUCT_LINKS: Record<string, { href: string; icon: IconName }> = {
  "Cross-venue screener": { href: "/products/screener", icon: "screener" },
  "Perpetuals monitor": { href: "/products/perpetuals", icon: "perpetuals" },
  "Triangular engine": { href: "/products/triangular", icon: "triangular" },
  "Automatic paper execution": { href: "/products/auto-paper", icon: "paper" },
};

export default function Home() {
  const page = loadCopy("home");
  const by = (t: string) => page.sections.find((s) => s.title === t);
  const hero = by("Hero");
  const sample = page.sections.find((s) => s.title.startsWith("Live screener sample"));
  const what = by("What it does");
  const why = by("Why paper first");
  const evidence = by("Evidence");
  const built = by("How it is built");
  const packages = by("Packages");
  const closing = by("Closing CTA");

  return (
    <>
      {hero && <Hero section={hero} secondaryHref="#evidence" />}

      {sample && (
        <section className="py-12 md:py-16">
          <Container>
            <p className="t-caption" dangerouslySetInnerHTML={{ __html: sample.fields["label above table"] ?? "" }} />
            <div className="mt-3 rounded border border-[var(--border)] bg-[var(--bg-panel)] p-6">
              {/* The live Watch-tier feed is wired in a later task; the
                  static export ships the copy's empty state, which is a
                  truthful reading in itself. */}
              <p className="t-body text-[var(--text-dim)]" dangerouslySetInnerHTML={{ __html: sample.fields["empty-state"] ?? "" }} />
            </div>
            <p className="prose t-small mt-3 max-w-none text-[var(--text-dim)]" dangerouslySetInnerHTML={{ __html: sample.fields["under-table"] ?? "" }} />
          </Container>
        </section>
      )}

      {what && (
        <section className="py-12 md:py-16">
          <Container>
            <h2 className="t-h2">{what.title}</h2>
            <div className="mt-8 grid gap-6 md:grid-cols-2">
              {what.subsections.map((s) => {
                const link = PRODUCT_LINKS[s.title];
                return (
                  <Link
                    key={s.title}
                    href={link?.href ?? "/"}
                    className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-5 hover:border-[var(--border-strong)]"
                  >
                    <div className="flex items-center gap-2">
                      <Icon name={link?.icon ?? "screener"} size={24} className="text-[var(--accent)]" />
                      <h3 className="t-h3">{s.title}</h3>
                    </div>
                    <div className="prose mt-2 text-[var(--text-dim)]" dangerouslySetInnerHTML={{ __html: s.html }} />
                  </Link>
                );
              })}
            </div>
          </Container>
        </section>
      )}

      {why && <CopySection section={why} />}
      {evidence && <Evidence section={evidence} />}
      {built && <CopySection section={built} />}
      {packages && <CopySection section={packages} />}

      {closing && (
        <section className="band border-y border-[var(--border)] py-16">
          <Container>
            <h2 className="t-h2" dangerouslySetInnerHTML={{ __html: closing.fields["h2"] ?? "" }} />
            <p className="t-lead mt-4 max-w-[68ch] text-[var(--text-dim)]" dangerouslySetInnerHTML={{ __html: closing.fields["body"] ?? "" }} />
            <div className="mt-6">
              <CtaButton html={closing.fields["cta"] ?? "Create an organisation"} />
            </div>
          </Container>
        </section>
      )}
    </>
  );
}
