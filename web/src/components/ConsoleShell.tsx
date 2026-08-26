"use client";

import Link from "next/link";
import type { ReactNode } from "react";
import { api, type SystemStatus } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth } from "@/lib/auth";

// Full navigation per SKILL.md §31, grouped per the UX audit's five-group
// IA (console-ux-audit.md §2). Sections without a page yet render as
// disabled entries — the console never pretends a page exists.
interface NavItem {
  label: string;
  href?: string;
  note?: string; // shown as the disabled entry's title/tooltip
}
interface NavGroup {
  title: string;
  items: NavItem[];
}

const GROUPS: NavGroup[] = [
  {
    title: "Operate",
    items: [
      { label: "Overview", href: "/overview" },
      { label: "Scanner", href: "/scanner" },
      { label: "Triangles", href: "/triangles" },
      { label: "Opportunities", href: "/opportunities" },
      { label: "Paper Trading", href: "/paper" },
    ],
  },
  {
    title: "Portfolio",
    items: [
      { label: "Portfolio & Balances", href: "/portfolio" },
      { label: "PnL & Analytics" },
      { label: "Orders" },
      { label: "Fills" },
    ],
  },
  {
    title: "Research",
    items: [
      { label: "Campaigns", href: "/campaigns" },
      { label: "Replay & Backtesting", href: "/replay" },
      { label: "AI Advisor", href: "/ai" },
    ],
  },
  {
    title: "Control",
    items: [
      { label: "Strategies", href: "/strategies" },
      { label: "Risk Center", href: "/risk" },
      { label: "Alerts", href: "/alerts" },
      { label: "Reports", href: "/reports" },
    ],
  },
  {
    title: "System",
    items: [
      { label: "Exchanges", href: "/exchanges" },
      { label: "Markets", href: "/settings#markets" },
      { label: "System Health", href: "/system" },
      { label: "Audit Log", href: "/audit" },
      { label: "Telegram" },
      { label: "Users & Security", href: "/settings#users" },
    ],
  },
];

// modeCopy maps the operational mode to its persistent-banner text and
// tone, per the audit's status vocabulary (§4.1). PAPER=ok(green),
// RECORD=accent(blue), REPLAY/BACKTEST=warn(amber), MARKET_DATA/SHADOW=dim.
const MODE_COPY: Record<string, { color: string; text: string }> = {
  PAPER: {
    color: "var(--ok)",
    text: "PAPER TRADING ONLY — live execution permanently disabled",
  },
  RECORD: {
    color: "var(--accent)",
    text: "RECORD — capturing public market data only, no orders placed",
  },
  REPLAY: { color: "var(--warn)", text: "REPLAY — replaying recorded data, not live" },
  BACKTEST: { color: "var(--warn)", text: "BACKTEST — historical simulation, not live" },
  MARKET_DATA: { color: "var(--text-dim)", text: "MARKET_DATA — market data only, no trading" },
  SHADOW: { color: "var(--text-dim)", text: "SHADOW — shadow evaluation, no orders placed" },
};

function ModeBanner() {
  const status = usePoll<SystemStatus>(() => api.system.status(), 10000);
  if (status.kind === "loading") {
    return (
      <div className="mb-3 rounded border border-[var(--border)] px-2 py-1.5 text-[11px] text-[var(--text-dim)]">
        Loading mode…
      </div>
    );
  }
  if (status.kind === "error") {
    // Chrome-level banner never shows an ErrorBox; degrade quietly — the
    // page body underneath still surfaces the real error.
    return (
      <div className="mb-3 rounded border border-[var(--border)] px-2 py-1.5 text-[11px] text-[var(--text-dim)]">
        Mode unknown (backend unreachable)
      </div>
    );
  }
  const mode = status.data.mode;
  const copy = MODE_COPY[mode] ?? {
    color: "var(--text-dim)",
    text: `${mode} — live execution permanently disabled`,
  };
  return (
    <div
      className="mb-3 flex items-start gap-2 rounded border px-2 py-1.5 text-[11px] font-medium leading-snug"
      style={{ borderColor: copy.color, color: copy.color }}
    >
      <span
        className="mt-0.5 inline-block h-2 w-2 shrink-0 rounded-full"
        style={{ background: copy.color }}
        aria-hidden
      />
      <span>{copy.text}</span>
    </div>
  );
}

export function ConsoleShell({ children, active }: { children: ReactNode; active: string }) {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col border-r border-[var(--border)] bg-[var(--bg-panel)] px-3 py-4">
        <div className="mb-2 px-2 text-sm font-semibold tracking-wide text-[var(--text)]">ARB CONSOLE</div>
        <div className="px-2">
          <ModeBanner />
        </div>
        <nav className="flex-1 space-y-3 overflow-y-auto text-[13px]">
          {GROUPS.map((group) => (
            <div key={group.title}>
              <div className="mb-1 px-2 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
                {group.title}
              </div>
              <div className="space-y-0.5">
                {group.items.map((item) => {
                  // Audit Log is visible to OPERATOR/ADMIN only (backend
                  // PermViewAudit); annotate rather than silently 403 a
                  // VIEWER who clicks through.
                  const restrictedForViewer = item.label === "Audit Log" && role === "VIEWER";
                  if (item.href && !restrictedForViewer) {
                    return (
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
                    );
                  }
                  return (
                    <span
                      key={item.label}
                      title={restrictedForViewer ? "Requires OPERATOR or ADMIN" : item.note ?? "Not implemented yet"}
                      className="block cursor-not-allowed rounded px-2 py-1 text-[var(--text-dim)] opacity-40"
                    >
                      {item.label}
                    </span>
                  );
                })}
              </div>
            </div>
          ))}
        </nav>
        <Link
          href="/settings"
          className={`mt-3 block rounded px-2 py-1 text-[13px] ${
            active === "Settings"
              ? "bg-[var(--bg-raised)] text-[var(--text)]"
              : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
          }`}
        >
          Settings
        </Link>
      </aside>
      <main className="min-w-0 flex-1 p-6">{children}</main>
    </div>
  );
}
