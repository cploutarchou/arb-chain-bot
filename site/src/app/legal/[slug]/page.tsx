import { notFound } from "next/navigation";
import { listLegal, loadLegal } from "@/lib/content";
import { pageMeta } from "@/lib/meta";
import { Container } from "@/components/Page";

export const dynamicParams = false;

export function generateStaticParams() {
  return listLegal().map((slug) => ({ slug }));
}

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  if (!listLegal().includes(slug)) return {};
  const doc = loadLegal(slug);
  return pageMeta(doc.title, doc.summary, `/legal/${slug}`);
}

export default async function LegalPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  if (!listLegal().includes(slug)) notFound();
  const doc = loadLegal(slug);
  return (
    <Container className="py-12 md:py-16">
      <p className="t-caption uppercase tracking-wide">Legal</p>
      <h1 className="t-display mt-2">{doc.title}</h1>
      {doc.data["status"] && (
        <p className="t-small mt-3 inline-block rounded border border-[var(--warn)] px-3 py-1 text-[var(--warn)]">
          {doc.data["status"]}
        </p>
      )}
      <article className="prose mt-8" dangerouslySetInnerHTML={{ __html: doc.html }} />
    </Container>
  );
}
