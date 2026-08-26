import Link from "next/link";
import type { ReactNode } from "react";

// Full navigation per SKILL.md §31. Sections without a page yet render as
// disabled entries — the console never pretends a page exists.
const NAV: { label: string; href?: string }[] = [
  { label: "Overview", href: "/overview" },
  { label: "Scanner", href: "/scanner" },
  { label: "Triangles", href: "/triangles" },
  { label: "Opportunities", href: "/opportunities" },
  { label: "Paper Trading", href: "/paper" },
  { label: "Portfolio", href: "/portfolio" },
  { label: "Exchanges", href: "/exchanges" },
  { label: "Strategies", href: "/strategies" },
  { label: "AI Advisor", href: "/ai" },
  { label: "Risk Center", href: "/risk" },
  { label: "Replay & Backtesting", href: "/replay" },
  { label: "Campaigns", href: "/campaigns" },
  { label: "Reports", href: "/reports" },
  { label: "Alerts", href: "/alerts" },
  { label: "System Health", href: "/system" },
  { label: "Audit Log", href: "/audit" },
  { label: "Settings", href: "/settings" },
];

export function ConsoleShell({ children, active }: { children: ReactNode; active: string }) {
  return (
    <div className="flex min-h-screen">
      <aside className="w-56 shrink-0 border-r border-[var(--border)] bg-[var(--bg-panel)] px-3 py-4">
        <div className="mb-4 px-2 text-sm font-semibold tracking-wide text-[var(--text)]">
          ARB CONSOLE
          <span className="mt-1 block text-[10px] font-normal uppercase tracking-wider text-[var(--text-dim)]">
            paper trading only
          </span>
        </div>
        <nav className="space-y-0.5 text-[13px]">
          {NAV.map((item) =>
            item.href ? (
              <Link
                key={item.label}
                href={item.href}
                className={`block rounded px-2 py-1 ${
                  active === item.label
                    ? "bg-[var(--bg-raised)] text-[var(--text)]"
                    : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
                }`}
              >
                {item.label}
              </Link>
            ) : (
              <span
                key={item.label}
                title="Not implemented yet"
                className="block cursor-not-allowed rounded px-2 py-1 text-[var(--text-dim)] opacity-40"
              >
                {item.label}
              </span>
            ),
          )}
        </nav>
      </aside>
      <main className="min-w-0 flex-1 p-6">{children}</main>
    </div>
  );
}
