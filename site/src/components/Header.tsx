import Link from "next/link";
import { brand } from "@/lib/tokens";
import { siteConfig } from "../../site.config";
import { ThemeToggle } from "./ThemeToggle";
import { Icon } from "./Icons";

const NAV = [
  { href: "/products/screener", label: "Screener" },
  { href: "/products/perpetuals", label: "Perpetuals" },
  { href: "/products/triangular", label: "Triangular" },
  { href: "/products/auto-paper", label: "Auto-paper" },
  { href: "/pricing", label: "Pricing" },
  { href: "/docs", label: "Docs" },
  { href: "/faq", label: "FAQ" },
];

export function Header() {
  return (
    <header className="border-b border-[var(--border)] bg-[var(--bg-panel)]">
      <div className="mx-auto flex max-w-[1120px] items-center gap-4 px-4 py-3 md:px-6">
        <Link href="/" className="flex items-center gap-2 font-semibold">
          <Icon name="screener" size={20} className="text-[var(--accent)]" />
          <span>{brand}</span>
        </Link>
        {/* Mobile: a native details/summary menu, no JS. */}
        <details className="relative ml-auto md:hidden">
          <summary
            aria-label="Menu"
            className="flex h-9 w-9 cursor-pointer list-none items-center justify-center rounded border border-[var(--border-strong)]"
          >
            <Icon name="menu" size={16} />
          </summary>
          <nav className="absolute right-0 z-40 mt-2 w-56 rounded border border-[var(--border-strong)] bg-[var(--bg-panel)] p-2 shadow-lg">
            {NAV.map((n) => (
              <Link
                key={n.href}
                href={n.href}
                className="block rounded px-3 py-2 t-small hover:bg-[var(--bg-raised)]"
              >
                {n.label}
              </Link>
            ))}
            <a
              href={siteConfig.consoleUrl}
              className="mt-1 block rounded bg-[var(--accent)] px-3 py-2 t-small font-medium text-[var(--on-accent)]"
            >
              Start the trial
            </a>
          </nav>
        </details>
        <nav className="ml-auto hidden items-center gap-1 md:flex">
          {NAV.map((n) => (
            <Link
              key={n.href}
              href={n.href}
              className="rounded px-3 py-2 t-small text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
            >
              {n.label}
            </Link>
          ))}
        </nav>
        <div className="hidden items-center gap-2 md:flex">
          <ThemeToggle />
          <a
            href={siteConfig.consoleUrl}
            className="inline-flex h-10 items-center rounded bg-[var(--accent)] px-4 t-small font-medium text-[var(--on-accent)]"
          >
            Start the trial
          </a>
        </div>
        <div className="md:hidden">
          <ThemeToggle />
        </div>
      </div>
    </header>
  );
}
