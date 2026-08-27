import { listGuide, loadGuide } from "@/lib/content";
import { pageMeta } from "@/lib/meta";
import { Container } from "@/components/Page";
import { DocsNav } from "@/components/DocsNav";

export const metadata = pageMeta(
  "Documentation",
  "User guide for the Scanner Suite: screener, perpetuals, alert rules, auto-paper, reports, packages, venues.",
  "/docs",
);

export default function Docs() {
  const readme = loadGuide("README");
  const pages = listGuide().map((slug) => loadGuide(slug));
  return (
    <Container className="py-12 md:py-16">
      <div className="grid gap-10 md:grid-cols-[220px_1fr]">
        <DocsNav pages={pages.map((p) => ({ slug: p.slug, title: p.title }))} />
        <div>
          <h1 className="t-display">{readme.title}</h1>
          <article className="prose mt-8" dangerouslySetInnerHTML={{ __html: readme.html }} />
        </div>
      </div>
    </Container>
  );
}
