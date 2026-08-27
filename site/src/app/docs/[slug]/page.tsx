import { notFound } from "next/navigation";
import { listGuide, loadGuide } from "@/lib/content";
import { pageMeta } from "@/lib/meta";
import { Container } from "@/components/Page";
import { DocsNav } from "@/components/DocsNav";

export const dynamicParams = false;

export function generateStaticParams() {
  return listGuide().map((slug) => ({ slug }));
}

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  if (!listGuide().includes(slug)) return {};
  const doc = loadGuide(slug);
  return pageMeta(doc.title, doc.summary, `/docs/${slug}`);
}

export default async function DocPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  if (!listGuide().includes(slug)) notFound();
  const doc = loadGuide(slug);
  const pages = listGuide().map((s) => {
    const d = loadGuide(s);
    return { slug: d.slug, title: d.title };
  });
  return (
    <Container className="py-12 md:py-16">
      <div className="grid gap-10 md:grid-cols-[220px_1fr]">
        <DocsNav pages={pages} current={slug} />
        <div>
          <p className="t-caption uppercase tracking-wide">User guide</p>
          <h1 className="t-display mt-2">{doc.title}</h1>
          <article className="prose mt-8" dangerouslySetInnerHTML={{ __html: doc.html }} />
        </div>
      </div>
    </Container>
  );
}
