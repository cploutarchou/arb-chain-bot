"use client";

// NotificationBell + NotificationPanel (design-system.md §4.4, UX §2.5).
// Merges the alerts stream (api.alerts, active state) with the Scanner
// Suite rule-events stream (api.screener.events, telegram-sent only) into
// one preview panel. Opening the bell marks items "seen" — a local,
// client-side-only concept for badge hygiene — it never acks/resolves an
// alert and never marks a rule event as anything on the backend; Ack/
// Resolve stay exclusively on the Alerts page.
//
// The screener stream is best-effort: a deployment without the Scanner
// Suite backend built in yet (404/503, same ABSENCE_CODES convention as
// ScreenerAwait) degrades to "alerts only," never an error banner in the
// chrome — this is a top-bar widget, not a page.

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { api, type Alert, type ScreenerEvent } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { Badge, severityTone } from "@/components/ui";
import { BellIcon } from "@/components/icons";

export interface NotificationItem {
  id: string;
  origin: "ALERT" | "RULE";
  severityBadge: {
    tone: "ok" | "warn" | "high" | "bad" | "dim";
    label: string;
  };
  summary: string;
  at: string;
  href: string;
}

function relTime(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "—";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

function fromAlert(a: Alert): NotificationItem {
  return {
    id: `alert:${a.id}`,
    origin: "ALERT",
    severityBadge: { tone: severityTone(a.severity), label: a.severity },
    summary: a.title,
    at: a.last_at,
    href: "/alerts",
  };
}

function fromRuleEvent(e: ScreenerEvent): NotificationItem {
  return {
    id: `rule:${e.id}`,
    origin: "RULE",
    severityBadge: {
      tone: e.closed_at ? "dim" : "warn",
      label: e.closed_at ? "closed" : "open",
    },
    summary: `${e.base}/${e.quote} ${e.buy_venue} → ${e.sell_venue}`,
    at: e.opened_at,
    href: "/scanner-alerts",
  };
}

// useScreenerRuleEvents: best-effort, silent-on-absence fetch of recent
// telegram-sent rule events — separate from usePoll's PollState error
// branch since a missing Scanner Suite backend must never surface here.
function useScreenerRuleEvents(): ScreenerEvent[] {
  const [events, setEvents] = useState<ScreenerEvent[]>([]);
  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const res = await api.screener.events("", 50);
        if (cancelled) return;
        setEvents((res.events ?? []).filter((e) => e.telegram_sent));
      } catch {
        if (!cancelled) setEvents([]);
      }
    };
    load();
    const t = setInterval(load, 30000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, []);
  return events;
}

// useNotificationItems is the shell's single source of the merged
// alerts+rule-events stream — call it ONCE in ConsoleShell and pass the
// result to every <NotificationBell> instance (mobile top bar + desktop
// sidebar both mount unconditionally, only CSS-hidden per breakpoint —
// same reason useModeState hoists its own hub subscription "for the
// whole shell, not one per mount"). A second usePoll/interval pair per
// bell instance would double every alerts/screener-events request on
// every page.
export function useNotificationItems(): NotificationItem[] {
  const alertsState = usePoll(() => api.alerts.list("active", 50), 15000);
  const ruleEvents = useScreenerRuleEvents();

  const alertItems =
    alertsState.kind === "ready"
      ? (alertsState.data.alerts ?? []).map(fromAlert)
      : [];
  const ruleItems = ruleEvents.map(fromRuleEvent);
  return [...alertItems, ...ruleItems].sort(
    (a, b) => new Date(b.at).getTime() - new Date(a.at).getTime(),
  );
}

export function NotificationBell({
  items: all,
}: {
  items: NotificationItem[];
}) {
  const [open, setOpen] = useState(false);
  const [seenIds, setSeenIds] = useState<Set<string>>(new Set());
  const btnRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  // CRITICAL items pinned to the top and never scrolled out of the
  // visible 8 (UX §2.5); everything else fills the remainder in recency
  // order.
  const critical = all.filter((i) => i.severityBadge.label === "CRITICAL");
  const rest = all.filter((i) => i.severityBadge.label !== "CRITICAL");
  const visible = [...critical, ...rest].slice(0, Math.max(8, critical.length));
  const overflow = all.length - visible.length;

  const unseenCount = all.filter((i) => !seenIds.has(i.id)).length;
  const anyUnseenCritical = all.some(
    (i) => !seenIds.has(i.id) && i.severityBadge.label === "CRITICAL",
  );

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        btnRef.current?.focus();
      }
    };
    const onClick = (e: MouseEvent) => {
      if (
        panelRef.current?.contains(e.target as Node) ||
        btnRef.current?.contains(e.target as Node)
      )
        return;
      setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onClick);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onClick);
    };
  }, [open]);

  const toggle = () => {
    setOpen((v) => {
      const next = !v;
      if (next)
        setSeenIds((prev) => new Set([...prev, ...all.map((i) => i.id)]));
      return next;
    });
  };

  return (
    <div className="relative">
      <button
        ref={btnRef}
        type="button"
        onClick={toggle}
        aria-label={`Notifications, ${unseenCount} unseen`}
        aria-expanded={open}
        className="relative flex h-8 w-8 items-center justify-center rounded text-[var(--text-dim)] hover:text-[var(--text)]"
      >
        <BellIcon />
        {unseenCount > 0 && (
          <span
            aria-hidden
            className={`absolute right-0.5 top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[11px] font-semibold ${
              anyUnseenCritical
                ? "bg-[var(--critical)] text-[var(--on-critical)]"
                : "bg-[var(--accent)] text-[var(--on-accent)]"
            }`}
          >
            {unseenCount > 99 ? "99+" : unseenCount}
          </span>
        )}
      </button>
      {open && (
        <div
          ref={panelRef}
          role="region"
          aria-live="polite"
          aria-label="Notifications"
          className="absolute right-0 top-full z-50 mt-1 w-[360px] max-w-[90vw] rounded border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-[0_0_0_1px_var(--border),0_8px_24px_var(--shadow-color)]"
        >
          <div className="flex h-9 items-center justify-between border-b border-[var(--border)] px-3">
            <span className="text-[10px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
              Notifications
            </span>
            <button
              type="button"
              onClick={() => setSeenIds(new Set(all.map((i) => i.id)))}
              className="text-[12px] text-[var(--accent)] hover:underline"
            >
              Mark all seen
            </button>
          </div>
          {visible.length === 0 ? (
            <p className="px-3 py-4 text-[13px] text-[var(--text-dim)]">
              No unresolved alerts or rule events.
            </p>
          ) : (
            <ul>
              {visible.map((item) => {
                const unseen = !seenIds.has(item.id);
                return (
                  <li
                    key={item.id}
                    className={`relative border-b border-[var(--border)] px-3 py-2 hover:bg-[var(--bg-raised)]`}
                  >
                    {unseen && (
                      <span
                        aria-hidden
                        className="absolute left-0 top-0 h-full w-0.5 bg-[var(--accent)]"
                      />
                    )}
                    <div className="flex items-center gap-2">
                      <Badge tone={item.severityBadge.tone}>
                        {item.severityBadge.label}
                      </Badge>
                      <Badge tone="dim">{item.origin}</Badge>
                      <span
                        className={`flex-1 truncate text-[13px] ${unseen ? "font-medium text-[var(--text)]" : "text-[var(--text)]"}`}
                      >
                        {item.summary}
                      </span>
                      <span className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">
                        {relTime(item.at)}
                      </span>
                    </div>
                    <Link
                      href={item.href}
                      onClick={() => setOpen(false)}
                      className="mt-0.5 block text-[12px] text-[var(--accent)] hover:underline"
                    >
                      {item.origin === "ALERT" ? "→ Alerts" : "→ Alert Rules"}
                    </Link>
                  </li>
                );
              })}
            </ul>
          )}
          <div className="flex h-9 items-center justify-between px-3 text-[12px]">
            {overflow > 0 ? (
              <span className="text-[var(--text-dim)]">
                +{overflow} more — view all
              </span>
            ) : (
              <span />
            )}
            <Link
              href="/alerts"
              onClick={() => setOpen(false)}
              className="text-[var(--accent)] hover:underline"
            >
              View all in Alerts →
            </Link>
          </div>
        </div>
      )}
    </div>
  );
}
