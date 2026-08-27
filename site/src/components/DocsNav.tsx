import Link from "next/link";

export function DocsNav({ pages, current }: { pages: { slug: string; title: string }[]; current?: string }) {
  return (
    <nav aria-label="Documentation" className="t-small">
      <Link href="/docs" className={`block rounded px-3 py-1.5 ${!current ? "bg-[var(--bg-raised)] font-medium" : "text-[var(--text-dim)]"}`}>
        Overview
      </Link>
      {pages.map((p) => (
        <Link
          key={p.slug}
          href={`/docs/${p.slug}`}
          className={`block rounded px-3 py-1.5 ${current === p.slug ? "bg-[var(--bg-raised)] font-medium" : "text-[var(--text-dim)]"}`}
        >
          {p.title}
        </Link>
      ))}
    </nav>
  );
}
