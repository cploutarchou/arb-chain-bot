import { loadCopy } from "@/lib/content";
import { pageMeta } from "@/lib/meta";
import { Container, CopySection, Tail } from "@/components/Page";

export const metadata = pageMeta(
  "About",
  "A measurement tool that files its own reports. Who runs it and how it is built.",
  "/about",
);

export default function About() {
  const page = loadCopy("about");
  return (
    <>
      <section className="band border-b border-[var(--border)]">
        <Container className="py-16 md:py-24">
          <div className="prose max-w-[68ch] [&>h2]:t-display [&>h2]:mt-0" dangerouslySetInnerHTML={{ __html: page.preambleHtml }} />
        </Container>
      </section>
      {page.sections.map((s) => (
        <CopySection key={s.title} section={s} />
      ))}
      <Tail html={page.tailHtml} />
    </>
  );
}
