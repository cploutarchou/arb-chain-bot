"use client";

import Link from "next/link";
import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  api,
  ApiError,
  type RecorderStatus,
  type ScannerStatus,
  type SystemStatus,
} from "@/lib/api/client";
import { usePoll, type PollState } from "@/lib/usePoll";
import { connectHub, type HubMessage } from "@/lib/ws";
import { useAuth, useEntitlement, can } from "@/lib/auth";
import { Button, ConfirmDialog } from "@/components/ui";
import { MoonIcon, NavIcon, SunIcon } from "@/components/icons";
import { IconRail, NavGroupHeader } from "@/components/IconRail";
import { GatedControl } from "@/components/GatedControl";
import { PaperControl } from "@/components/PaperControl";
import {
  NotificationBell,
  useNotificationItems,
} from "@/components/NotificationBell";

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
      { label: "PnL & Analytics", href: "/pnl" },
      { label: "Orders", href: "/orders" },
      { label: "Fills", href: "/fills" },
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
      { label: "Telegram", href: "/telegram" },
      { label: "Users & Security", href: "/settings#users" },
    ],
  },
  // Scanner Suite (T-065..T-072, docs/design/scanner-suite.md §5): cross-
  // venue spot screener, perpetuals/funding monitor, spreads calculator,
  // alert rules, automatic PAPER execution — public market data only.
  {
    title: "Scanner Suite",
    items: [
      { label: "Screener", href: "/screener" },
      { label: "Perpetuals", href: "/perpetuals" },
      { label: "Funding", href: "/funding" },
      { label: "Calculator", href: "/calculator" },
      { label: "Alert Rules", href: "/scanner-alerts" },
      // Distinct label from the Control group's existing "Reports" item
      // (engine daily/weekly ops reports, a different system per
      // docs/user-guide/reports.md's "two report systems ... do not
      // confuse them") — both GLYPHS and NavContent's activeGroupTitle
      // lookup key off this exact string, so it must not collide.
      { label: "Screener Reports", href: "/screener-reports" },
      { label: "Auto-Paper", href: "/auto-paper" },
    ],
  },
];

// GROUP_STORAGE_KEY persists which nav groups are collapsed, per
// operator browser (arbitragescanner-style icon sidebar, design §0 item
// 6 / §5). Reading/writing localStorage is wrapped in try/catch — a
// private-mode or quota failure degrades to "always expanded", never a
// crash.
const GROUP_STORAGE_KEY = "arb.nav.collapsed-groups";

function loadCollapsedGroups(): Set<string> {
  try {
    const raw = localStorage.getItem(GROUP_STORAGE_KEY);
    if (!raw) return new Set();
    const arr = JSON.parse(raw) as unknown;
    return Array.isArray(arr)
      ? new Set(arr.filter((v): v is string => typeof v === "string"))
      : new Set();
  } catch {
    return new Set();
  }
}

function saveCollapsedGroups(groups: Set<string>) {
  try {
    localStorage.setItem(GROUP_STORAGE_KEY, JSON.stringify([...groups]));
  } catch {
    // Private mode / quota exceeded — the toggle still works for this
    // page load, it just won't persist across a reload.
  }
}

// useCollapsedGroups: state loads from localStorage in an effect (never
// during render — that would be a hydration mismatch between server and
// client markup), defaults to fully expanded, and never actually
// collapses the group containing the current page so the active link is
// always reachable without an extra click.
function useCollapsedGroups(activeGroupTitle: string | undefined) {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  useEffect(() => {
    setCollapsed(loadCollapsedGroups());
  }, []);
  const toggle = (title: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(title)) next.delete(title);
      else next.add(title);
      saveCollapsedGroups(next);
      return next;
    });
  };
  // expand: used by the icon rail (UX §2.1 — a rail click expands/scrolls
  // to its group, it does not collapse the others; there is no forced
  // one-group-open accordion here, since that would hide every other
  // group's links on first load, which e2e/console.spec.ts's "nav group
  // renders and every Scanner Suite page loads" relies on staying true).
  const expand = (title: string) => {
    setCollapsed((prev) => {
      if (!prev.has(title)) return prev;
      const next = new Set(prev);
      next.delete(title);
      saveCollapsedGroups(next);
      return next;
    });
  };
  const isCollapsed = (title: string) =>
    collapsed.has(title) && title !== activeGroupTitle;
  return { isCollapsed, toggle, expand };
}

function groupDomId(title: string): string {
  return `nav-group-${title.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`;
}

// useThemeToggle: light/dark persisted in localStorage as data-theme on
// <html> (globals.css §"theme tokens" — :root is light by default,
// :root[data-theme="dark"] and the prefers-color-scheme media query both
// carry the dark values). No explicit choice yet: this only reads state
// for the toggle's own icon/aria-pressed, it does not write an attribute
// — the CSS media query stays authoritative until the operator picks a
// side. try/catch around every localStorage call (private mode/quota).
function useThemeToggle() {
  const [theme, setThemeState] = useState<"light" | "dark">("dark");
  useEffect(() => {
    let stored: string | null = null;
    try {
      stored = localStorage.getItem("arb.theme");
    } catch {
      stored = null;
    }
    if (stored === "light" || stored === "dark") {
      setThemeState(stored);
      document.documentElement.setAttribute("data-theme", stored);
      return;
    }
    const prefersDark =
      typeof window !== "undefined" && window.matchMedia
        ? window.matchMedia("(prefers-color-scheme: dark)").matches
        : true;
    setThemeState(prefersDark ? "dark" : "light");
  }, []);
  const toggle = () => {
    setThemeState((prev) => {
      const next = prev === "dark" ? "light" : "dark";
      document.documentElement.setAttribute("data-theme", next);
      try {
        localStorage.setItem("arb.theme", next);
      } catch {
        // Private mode / quota exceeded — the attribute still applies
        // for the remainder of this session.
      }
      return next;
    });
  };
  return { theme, toggle };
}

function ThemeToggle() {
  const { theme, toggle } = useThemeToggle();
  return (
    <button
      type="button"
      onClick={toggle}
      aria-label={
        theme === "dark" ? "Switch to light theme" : "Switch to dark theme"
      }
      title={
        theme === "dark" ? "Switch to light theme" : "Switch to dark theme"
      }
      className="rounded border border-[var(--border)] p-1.5 text-[var(--text-dim)] hover:text-[var(--text)]"
    >
      {theme === "dark" ? <SunIcon /> : <MoonIcon />}
    </button>
  );
}

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
  REPLAY: {
    color: "var(--warn)",
    text: "REPLAY — replaying recorded data, not live",
  },
  BACKTEST: {
    color: "var(--warn)",
    text: "BACKTEST — historical simulation, not live",
  },
  MARKET_DATA: {
    color: "var(--text-dim)",
    text: "MARKET_DATA — market data only, no trading",
  },
  SHADOW: {
    color: "var(--text-dim)",
    text: "SHADOW — shadow evaluation, no orders placed",
  },
};

// useModeState hoists the RUNNING mode (REST, already polled once for
// both sidebar mounts — mobile overlay + desktop, T-057) and layers the
// hub "health" topic on top purely for the CONFIGURED mode (T-059 §2.3:
// {running, configured}), so the annotation degrades to nothing on a
// profile with no supervisor/engine rather than going permanently blank.
// One connectHub subscription for the whole shell, not one per mount.
function useModeState(): {
  status: PollState<SystemStatus>;
  configured: string | null;
} {
  const status = usePoll<SystemStatus>(() => api.system.status(), 10000);
  const [configured, setConfigured] = useState<string | null>(null);
  useEffect(() => {
    return connectHub(["health"], {
      onMessage: (msg: HubMessage) => {
        if (msg.topic !== "health") return;
        const data = msg.data as
          { mode?: { running?: string; configured?: string } } | undefined;
        if (data?.mode?.configured) setConfigured(data.mode.configured);
      },
    });
  }, []);
  return { status, configured };
}

// ModeBanner always renders the operating mode as visible text (never
// color alone, never hidden in a title tooltip) — the full form for the
// sidebar/overlay, and a `compact` dot+word form that fits the mobile top
// bar (F1: the mode must announce itself at every width, not just when
// the hamburger menu is open).
function ModeBanner({
  status,
  configured,
  compact,
}: {
  status: PollState<SystemStatus>;
  configured: string | null;
  compact?: boolean;
}) {
  if (status.kind === "loading") {
    return compact ? (
      <span className="text-[11px] text-[var(--text-dim)]">Mode…</span>
    ) : (
      <div className="mb-3 rounded border border-[var(--border)] px-2 py-1.5 text-[11px] text-[var(--text-dim)]">
        Loading mode…
      </div>
    );
  }
  if (status.kind === "error") {
    // Chrome-level banner never shows an ErrorBox; degrade quietly — the
    // page body underneath still surfaces the real error.
    return compact ? (
      <span className="text-[11px] text-[var(--text-dim)]">
        Mode unknown
      </span>
    ) : (
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
  // T-059 §2.3: the banner shows the RUNNING mode and, when the platform-
  // settings document has been changed but not yet applied (restart
  // pending), annotates it with the CONFIGURED one.
  const showsPending = configured && configured !== mode;
  if (compact) {
    return (
      <span
        className="inline-flex min-w-0 items-center gap-1.5 rounded border px-1.5 py-0.5 text-[11px] font-semibold leading-none"
        style={{ borderColor: copy.color, color: copy.color }}
        title={copy.text}
      >
        <span
          className="inline-block h-1.5 w-1.5 shrink-0 rounded-full"
          style={{ background: copy.color }}
          aria-hidden
        />
        <span className="truncate">{mode}</span>
        {showsPending && (
          <span className="shrink-0 text-[var(--warn)]" aria-label="restart pending">
            *
          </span>
        )}
      </span>
    );
  }
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
      <span>
        {copy.text}
        {showsPending && (
          <span className="ml-1 block font-normal text-[var(--warn)]">
            configured: {configured} — restart pending
          </span>
        )}
      </span>
    </div>
  );
}

// RestartBanner (T-057, design §4): layout-level, driven by
// GET /api/v1/engine/status. Every state string it shows is rendered
// verbatim from the backend (state, pending_reasons, error) — never a
// frontend-composed sentence, and never a neutral placeholder for an
// unknown state.
function RestartBanner() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayRestart = can(role, "system:config");

  const [refresh, setRefresh] = useState(0);
  const status = usePoll(() => api.engine.status(), 10000, [refresh]);

  const [dialogOpen, setDialogOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [reason, setReason] = useState("");
  const [stopRecording, setStopRecording] = useState(false);
  const [recorder, setRecorder] = useState<RecorderStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const openDialog = async () => {
    setTyped("");
    setReason("");
    setStopRecording(false);
    setErr("");
    setRecorder(null);
    setDialogOpen(true);
    try {
      const res = await api.recordings.list();
      setRecorder(res.recorder ?? null);
    } catch {
      // Recorder status is only used to decide whether to show the
      // "stop recording" checkbox; failing to load it just hides that
      // option rather than blocking the restart dialog.
    }
  };

  const confirmRestart = async () => {
    setBusy(true);
    setErr("");
    try {
      await api.engine.restart({
        confirm: typed,
        reason: reason || undefined,
        stop_recording: stopRecording,
      });
      setDialogOpen(false);
      setRefresh((n) => n + 1);
    } catch (e: unknown) {
      setErr(e instanceof ApiError ? e.message : "Restart request failed.");
    } finally {
      setBusy(false);
    }
  };

  // Chrome-level: never an ErrorBox, never a loading flicker — degrade
  // quietly like ModeBanner (also covers the API-profile 404 engine_absent
  // case, where there is honestly no restart surface to show).
  if (status.kind !== "ready") return null;
  const restart = status.data.restart;
  if (restart.state === "ready" && (restart.pending_reasons?.length ?? 0) === 0)
    return null;

  const reasons = restart.pending_reasons?.join("; ") ?? "";
  const tone =
    restart.state === "failed"
      ? "var(--critical)"
      : restart.state === "restarting"
        ? "var(--warn)"
        : "var(--warn)";

  return (
    <div
      className="mb-3 rounded border px-3 py-2 text-[12px]"
      style={{ borderColor: tone, color: tone }}
    >
      {restart.state === "pending" && (
        <div className="flex flex-wrap items-center gap-2">
          <span>
            <strong>Saved but not running:</strong> {reasons}. Restarting
            reconnects the market-data feed, rebuilds the triangles, and starts
            a new paper session — persisted history is kept; a paused paper
            engine stays paused.
          </span>
          {mayRestart && (
            <Button onClick={openDialog} danger>
              Restart engine…
            </Button>
          )}
        </div>
      )}
      {restart.state === "restarting" && (
        <span>
          Restarting — waiting for the last recording segment to close and the
          feed to drain…
        </span>
      )}
      {restart.state === "failed" && (
        <div className="flex flex-wrap items-center gap-2">
          <span>
            <strong>Restart failed:</strong> {restart.error || "unknown error"}
          </span>
          {mayRestart && (
            <>
              <Button onClick={openDialog} danger>
                Restart engine…
              </Button>
              <Link
                href="/settings#platform-versions"
                className="text-[var(--accent)] underline"
              >
                Roll back to v{restart.settings_version} in Settings
              </Link>
            </>
          )}
        </div>
      )}

      {dialogOpen && (
        <ConfirmDialog
          title="Restart the engine?"
          danger
          confirmLabel={busy ? "Restarting…" : "Restart engine"}
          confirmDisabled={typed !== "RESTART" || busy}
          onConfirm={confirmRestart}
          onCancel={() => setDialogOpen(false)}
          body={
            <div>
              <p className="mb-3">
                This cancels the running engine, waits for it to stop, and
                re-enters it with the current settings. In-memory paper balances
                reset to the configured starting balances; persisted
                opportunities, cycles and reports are kept. A paused paper
                engine stays paused.
              </p>
              {recorder?.running && (
                <label className="mb-3 flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={stopRecording}
                    onChange={(e) => setStopRecording(e.target.checked)}
                  />
                  Stop the active recording (
                  {recorder.session_id ?? "unknown session"}) and restart
                </label>
              )}
              <label
                className="mb-1 block text-[12px] text-[var(--text-dim)]"
                htmlFor="restart-reason"
              >
                Reason (optional)
              </label>
              <input
                id="restart-reason"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                className="mb-3 w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
              />
              <label
                className="mb-1 block text-[12px] text-[var(--text-dim)]"
                htmlFor="restart-confirm"
              >
                Type <strong>RESTART</strong> to confirm:
              </label>
              <input
                id="restart-confirm"
                autoFocus
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                spellCheck={false}
                className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
              />
              {err && <p className="mt-2 text-[var(--critical)]">{err}</p>}
            </div>
          }
        />
      )}
    </div>
  );
}

// NavContent is the sidebar's inner nav — shared between the always-
// visible desktop sidebar and the mobile overlay (§4.7/BL-24: below md
// the sidebar collapses to a top bar with a hamburger revealing this
// same nav as a full-height overlay, closing on nav or outside-tap).
// `rail`: desktop-only (UX §8 — below md the overlay always shows full
// labels, never icon-only) — renders the IconRail (design-system.md
// §4.1) beside the label column, sharing one collapsed-groups state.
// Groups stay default-expanded (no forced one-open accordion — UX §2.1's
// "an operator can pin more than one open" reads as *not* mandating a
// single-group accordion by default, and a default accordion would hide
// every Scanner Suite link e2e/console.spec.ts expects visible from a
// fresh session); a rail click expands/scrolls to its group without
// collapsing the others.
function NavContent({
  active,
  role,
  onNavigate,
  rail,
}: {
  active: string;
  role?: string;
  onNavigate?: () => void;
  rail?: boolean;
}) {
  const activeGroupTitle = GROUPS.find((g) =>
    g.items.some((i) => i.label === active),
  )?.title;
  const { isCollapsed, toggle, expand } = useCollapsedGroups(activeGroupTitle);
  const groupRefs = useRef<Record<string, HTMLDivElement | null>>({});
  // Auto-Paper is the UX spec's own package-gating example (console-v2.md
  // §2.1: "Auto-Paper 🔒Pro") — Watch's auto_paper.strategies is empty
  // (packages.md §2 "none (manual paper only)"), so an organisation on
  // Watch sees the nav item as package-gated rather than a plain link.
  // undefined (auth still loading, or anonymous) never gates optimistically.
  const autoPaperStrategies = useEntitlement("auto_paper.strategies");
  const autoPaperGated =
    autoPaperStrategies !== undefined && autoPaperStrategies.length === 0;

  const labelColumn = (
    <>
      <nav
        className="flex-1 space-y-3 overflow-y-auto text-[13px]"
        aria-label="Primary"
      >
        {GROUPS.map((group) => {
          const collapsedNow = isCollapsed(group.title);
          const domId = groupDomId(group.title);
          return (
            <div
              key={group.title}
              ref={(el) => {
                groupRefs.current[group.title] = el;
              }}
            >
              <NavGroupHeader
                title={group.title}
                collapsed={collapsedNow}
                onToggle={() => toggle(group.title)}
                controlsId={domId}
              />
              {!collapsedNow && (
                <div id={domId} className="space-y-0.5">
                  {group.items.map((item) => {
                    // Audit Log is visible to OPERATOR/ADMIN only (backend
                    // PermViewAudit); annotate rather than silently 403 a
                    // VIEWER who clicks through.
                    const restrictedForViewer =
                      item.label === "Audit Log" && role === "VIEWER";
                    const packageGated =
                      item.label === "Auto-Paper" && autoPaperGated;
                    if (item.href && !restrictedForViewer && !packageGated) {
                      return (
                        <Link
                          key={item.label}
                          href={item.href}
                          onClick={onNavigate}
                          className={`flex items-center gap-2 rounded px-2 py-1 ${
                            active === item.label
                              ? "bg-[var(--bg-raised)] text-[var(--text)]"
                              : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
                          }`}
                        >
                          <NavIcon label={item.label} />
                          {item.label}
                        </Link>
                      );
                    }
                    if (packageGated) {
                      return (
                        <GatedControl
                          key={item.label}
                          as="nav"
                          state="package"
                          reason=""
                          icon={<NavIcon label={item.label} />}
                          upgradeHref="/billing"
                          packageName="Signal"
                        >
                          {item.label}
                        </GatedControl>
                      );
                    }
                    return (
                      <GatedControl
                        key={item.label}
                        as="nav"
                        state={restrictedForViewer ? "role" : "unbuilt"}
                        reason={
                          restrictedForViewer
                            ? "Requires OPERATOR or ADMIN"
                            : (item.note ?? "Not implemented yet")
                        }
                        icon={<NavIcon label={item.label} />}
                      >
                        {item.label}
                      </GatedControl>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}
      </nav>
      {/* Organisation and Billing (T-081/T-083): pinned like Settings, not
          inside a Scanner Suite/Operate group — this is the tenancy/
          account surface, reachable regardless of which product group is
          collapsed. Always shown to any authenticated account: /org is
          member-readable, /billing subscription is member-readable, and
          both routes' own pages gate mutation controls on OWNER/ADMIN. */}
      {["Organisation", "Billing"].map((label) => {
        const href = label === "Organisation" ? "/org" : "/billing";
        return (
          <Link
            key={label}
            href={href}
            onClick={onNavigate}
            className={`mt-1 flex items-center gap-2 rounded px-2 py-1 text-[13px] ${
              active === label
                ? "bg-[var(--bg-raised)] text-[var(--text)]"
                : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
            }`}
          >
            <NavIcon label={label} />
            {label}
          </Link>
        );
      })}
      <Link
        href="/settings"
        onClick={onNavigate}
        className={`mt-1 flex items-center gap-2 rounded px-2 py-1 text-[13px] ${
          active === "Settings"
            ? "bg-[var(--bg-raised)] text-[var(--text)]"
            : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
        }`}
      >
        <NavIcon label="Settings" />
        Settings
      </Link>
    </>
  );

  if (!rail) return labelColumn;

  return (
    <div className="flex min-h-0 flex-1 gap-2">
      <IconRail
        groups={GROUPS.map((g) => g.title)}
        activeGroupTitle={activeGroupTitle}
        onSelect={(title) => {
          expand(title);
          groupRefs.current[title]?.scrollIntoView({
            behavior: "smooth",
            block: "nearest",
          });
        }}
      />
      <div className="flex min-w-0 flex-1 flex-col">{labelColumn}</div>
    </div>
  );
}

export function ConsoleShell({
  children,
  active,
}: {
  children: ReactNode;
  active: string;
}) {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const [mobileOpen, setMobileOpen] = useState(false);
  const modeState = useModeState();
  // Hoisted once for the whole shell, same reasoning as modeState above:
  // the mobile top bar, the mobile overlay and the desktop sidebar each
  // mount their own PaperControl (CSS shows/hides them per breakpoint,
  // all three exist in the tree at once), and must not each open a
  // separate poll of the same endpoint.
  const paperStatus = usePoll<ScannerStatus>(() => api.scanner.status(), 5000);
  // One alerts+rule-events poll for the whole shell (see
  // useNotificationItems' own comment) — shared by both bell mounts
  // below, same pattern as modeState feeding both ModeBanner mounts.
  const notificationItems = useNotificationItems();

  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      {/* Mobile top bar (< md): hamburger reveals the full nav as an
          overlay; the desktop sidebar below is hidden at this width. A
          second row keeps the mode and the paper pause/resume control
          visible on every page at every width (F1/F2) — neither waits
          for the hamburger menu to open. */}
      <div className="border-b border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 md:hidden">
        <div className="flex items-center justify-between">
          <span className="text-sm font-semibold tracking-wide text-[var(--text)]">
            ARB CONSOLE
          </span>
          <div className="flex items-center gap-2">
            <NotificationBell items={notificationItems} />
            <ThemeToggle />
            <button
              type="button"
              aria-label={mobileOpen ? "Close navigation" : "Open navigation"}
              aria-expanded={mobileOpen}
              onClick={() => setMobileOpen((v) => !v)}
              className="rounded border border-[var(--border)] px-2 py-1 text-[13px] text-[var(--text)]"
            >
              {mobileOpen ? "Close ✕" : "Menu ☰"}
            </button>
          </div>
        </div>
        <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
          <ModeBanner
            status={modeState.status}
            configured={modeState.configured}
            compact
          />
          <PaperControl status={paperStatus} role={role} compact />
        </div>
      </div>
      {mobileOpen && (
        <div className="fixed inset-0 z-40 flex md:hidden">
          <div
            className="absolute inset-0 bg-[var(--overlay)]"
            onClick={() => setMobileOpen(false)}
            aria-hidden
          />
          <aside className="relative z-50 flex w-72 max-w-[85vw] flex-col overflow-y-auto border-r border-[var(--border)] bg-[var(--bg-panel)] px-3 py-4">
            <div className="mb-2 px-2 text-sm font-semibold tracking-wide text-[var(--text)]">
              ARB CONSOLE
            </div>
            <div className="px-2">
              <ModeBanner
                status={modeState.status}
                configured={modeState.configured}
              />
              <PaperControl status={paperStatus} role={role} />
            </div>
            <NavContent
              active={active}
              role={role}
              onNavigate={() => setMobileOpen(false)}
            />
          </aside>
        </div>
      )}

      <aside className="sticky top-0 hidden h-screen w-56 shrink-0 flex-col overflow-y-auto border-r border-[var(--border)] bg-[var(--bg-panel)] px-3 py-4 md:flex">
        <div className="mb-2 flex items-center justify-between px-2">
          <span className="text-sm font-semibold tracking-wide text-[var(--text)]">
            ARB CONSOLE
          </span>
          <div className="flex items-center gap-1">
            <NotificationBell items={notificationItems} />
            <ThemeToggle />
          </div>
        </div>
        <div className="px-2">
          <ModeBanner
            status={modeState.status}
            configured={modeState.configured}
          />
          <PaperControl status={paperStatus} role={role} />
        </div>
        <NavContent active={active} role={role} rail />
      </aside>
      <main className="min-w-0 flex-1 p-4 md:p-6">
        <RestartBanner />
        {children}
      </main>
    </div>
  );
}
