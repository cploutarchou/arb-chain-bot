import Link from "next/link";
import type { ReactNode } from "react";
import type { Section } from "@/lib/content";
import { siteConfig } from "../../site.config";
import { Icon } from "./Icons";

// Shared layout primitives for copy-driven pages.

export function Container({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`mx-auto max-w-[1120px] px-4 md:px-6 ${className}`}>{children}</div>;
}

/** Turns a CTA label (e.g. "Start a 14-day Operator trial — no card") into a link to the console. */
export function CtaButton({
  html,
  href = siteConfig.consoleUrl,
  secondary = false,
}: {
  html: string;
  href?: string;
  secondary?: boolean;
}) {
  const cls = secondary
    ? "inline-flex h-10 items-center gap-2 rounded border border-[var(--border-strong)] px-4 t-small font-medium text-[var(--text)] hover:bg-[var(--bg-raised)]"
    : "inline-flex h-10 items-center gap-2 rounded bg-[var(--accent)] px-4 t-small font-medium text-[var(--on-accent)]";
  const internal = href.startsWith("/");
  const inner = (
    <>
      <span dangerouslySetInnerHTML={{ __html: html }} />
      <Icon name="arrow" size={16} />
    </>
  );
  return internal ? (
    <Link href={href} className={cls}>{inner}</Link>
  ) : (
    <a href={href} className={cls}>{inner}</a>
  );
}

/** Hero built from the `**Eyebrow:** / **H1:** / **Sub:** / **CTA:**` fields of a "Hero" section. */
export function Hero({
  section,
  secondaryHref,
}: {
  section: Section;
  secondaryHref?: string;
}) {
  const f = section.fields;
  return (
    <section className="band border-b border-[var(--border)]">
      <Container className="py-16 md:py-24">
        {f["eyebrow"] && (
          <p className="t-caption uppercase tracking-wide" dangerouslySetInnerHTML={{ __html: f["eyebrow"] }} />
        )}
        {f["h1"] && (
          <h1 className="t-display mt-3 max-w-[68ch]" dangerouslySetInnerHTML={{ __html: f["h1"] }} />
        )}
        {f["sub"] && (
          <p className="t-lead mt-5 max-w-[68ch] text-[var(--text-dim)]" dangerouslySetInnerHTML={{ __html: f["sub"] }} />
        )}
        <div className="mt-8 flex flex-wrap gap-3">
          {(f["primary cta"] ?? f["cta"]) && <CtaButton html={(f["primary cta"] ?? f["cta"]) as string} />}
          {f["secondary cta"] && (
            <CtaButton html={f["secondary cta"]} href={secondaryHref ?? "#evidence"} secondary />
          )}
        </div>
        {f["under-cta line"] && (
          <p className="t-small mt-4 max-w-[68ch] text-[var(--text-dim)]" dangerouslySetInnerHTML={{ __html: f["under-cta line"] }} />
        )}
      </Container>
    </section>
  );
}

/** Generic `##` section: title + rendered markdown body. */
export function CopySection({ section, id }: { section: Section; id?: string }) {
  return (
    <section id={id} className="py-12 md:py-16">
      <Container>
        <h2 className="t-h2">{section.title}</h2>
        {section.subsections.length > 0 ? (
          <div className="mt-8 grid gap-6 md:grid-cols-2">
            {section.subsections.map((s) => (
              <div key={s.title} className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-5">
                <h3 className="t-h3">{s.title}</h3>
                <div className="prose mt-2 text-[var(--text-dim)]" dangerouslySetInnerHTML={{ __html: s.html }} />
              </div>
            ))}
          </div>
        ) : (
          <div className="prose mt-6" dangerouslySetInnerHTML={{ __html: section.html }} />
        )}
      </Container>
    </section>
  );
}

/** Evidence block: the only place a figure may appear (compliance item 4). */
export function Evidence({ section }: { section: Section }) {
  return (
    <section id="evidence" className="py-12 md:py-16">
      <Container>
        <h2 className="t-h2">{section.title}</h2>
        <div
          className="prose mt-6 max-w-[80ch] rounded border border-[var(--border-strong)] bg-[var(--bg-panel)] p-5 [&>blockquote]:border-0 [&>blockquote]:bg-transparent [&>blockquote]:p-0"
          dangerouslySetInnerHTML={{ __html: section.html }}
        />
        <p className="t-caption mt-3">
          Simulated on paper. Past paper performance is not a projection or
          guarantee of future results. Source reports live under
          docs/campaigns/ and are quoted with their fees and verdict.
        </p>
      </Container>
    </section>
  );
}

/** The trailing risk summary / footer line of a copy page. */
export function Tail({ html }: { html: string }) {
  if (!html) return null;
  return (
    <section className="py-8">
      <Container>
        <div
          className="prose max-w-[80ch] t-small text-[var(--text-dim)] [&>blockquote]:border-[var(--warn)]"
          dangerouslySetInnerHTML={{ __html: html }}
        />
      </Container>
    </section>
  );
}

/** Country banner for perpetuals surfaces (compliance item 8). */
export function JurisdictionBanner({ html }: { html?: string }) {
  return (
    <div role="note" className="border-b border-[var(--warn)] bg-[var(--bg-panel)]">
      <Container className="flex gap-3 py-3 t-small">
        <Icon name="info" size={20} className="mt-0.5 shrink-0 text-[var(--warn)]" />
        <div>
          <strong>Information only.</strong> Perpetual futures are derivatives
          and may not be available in your jurisdiction. This page describes a
          monitor; it does not promote or offer any derivative.
          {html && <span className="block mt-1 text-[var(--text-dim)]" dangerouslySetInnerHTML={{ __html: html }} />}
        </div>
      </Container>
    </div>
  );
}
