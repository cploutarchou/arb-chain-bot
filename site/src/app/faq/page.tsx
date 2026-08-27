import { loadCopy } from "@/lib/content";
import { pageMeta } from "@/lib/meta";
import { Container, Tail } from "@/components/Page";

export const metadata = pageMeta(
  "FAQ",
  "What the product is and is not, spreads and results, perpetuals, alerts, billing.",
  "/faq",
);

export default function Faq() {
  const page = loadCopy("faq");
  return (
    <>
      <section className="band border-b border-[var(--border)]">
        <Container className="py-16">
          <h1 className="t-display">Frequently asked questions</h1>
          <nav className="mt-6 flex flex-wrap gap-2 t-small">
            {page.sections.map((s) => (
              <a key={s.title} href={`#${slug(s.title)}`} className="rounded border border-[var(--border-strong)] px-3 py-1">
                {s.title}
              </a>
            ))}
          </nav>
        </Container>
      </section>
      {page.sections.map((s) => (
        <section key={s.title} id={slug(s.title)} className="py-10">
          <Container>
            <h2 className="t-h2">{s.title}</h2>
            <div className="prose mt-6 max-w-[72ch] [&>p>strong:only-child]:t-h3" dangerouslySetInnerHTML={{ __html: s.html }} />
          </Container>
        </section>
      ))}
      <Tail html={page.tailHtml} />
    </>
  );
}

function slug(s: string) {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/(^-|-$)/g, "");
}
