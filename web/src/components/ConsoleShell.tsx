"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState, type ReactNode } from "react";
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
import { GatedControl } from "@/components/GatedControl";
import { PaperControl } from "@/components/PaperControl";
import {
  NotificationBell,
  useNotificationItems,
} from "@/components/NotificationBell";

// Task-based navigation (client-area audit 2026-09-12 + refine command
// §3/§4A): six primary destinations replace the former 29-link group
// wall, with a destination's own routes shown as contextual secondary
// links only while it is active. Selection is ROUTE-based (pathname
// prefixes against stable ids), never label-based — renaming a label
// can no longer break active-state or e2e selection. Every previously
// reachable route stays reachable: it is either a destination landing,
// a secondary link, an Administration link, or a detail route reached
// through its parent page.
interface NavLink {
  label: string;
  href: string;
}
interface NavDestination {
  id: string; // stable selection/DOM id — never rename casually
  label: string;
  href: string; // the destination's landing route
  secondary: NavLink[]; // shown under the destination while active
  // Extra route prefixes (detail pages) that activate the destination
  // beyond href and the secondary hrefs.
  match: string[];
}

const DESTINATIONS: NavDestination[] = [
  {
    id: "overview",
    label: "Overview",
    href: "/overview",
    secondary: [],
    match: ["/overview"],
  },
  {
    // Cross-exchange screening leads; the triangular engine is a
    // distinctly-named sibling (audit §2C: users must not have to
    // guess "Scanner" vs "Screener").
    id: "discover",
    label: "Discover",
    href: "/screener",
    secondary: [
      { label: "Screener (cross-exchange)", href: "/screener" },
      { label: "Scanner (triangular)", href: "/scanner" },
      { label: "Perpetuals", href: "/perpetuals" },
      { label: "Funding", href: "/funding" },
      { label: "Calculator", href: "/calculator" },
      { label: "Triangles", href: "/triangles" },
      { label: "Opportunities", href: "/opportunities" },
    ],
    match: ["/triangles", "/opportunities"],
  },
  {
    // Both simulation families in one navigation home (audit §2E),
    // their APIs/ledgers/controls still distinct.
    id: "paper",
    label: "Paper Trading",
    href: "/paper",
    secondary: [
      { label: "Triangular simulations", href: "/paper" },
      { label: "Auto-Paper rules", href: "/auto-paper" },
      { label: "Portfolio & Balances", href: "/portfolio" },
      { label: "Orders", href: "/orders" },
      { label: "Fills", href: "/fills" },
    ],
    match: ["/cycles"],
  },
  {
    id: "research",
    label: "Research & Results",
    href: "/pnl",
    secondary: [
      { label: "PnL & Analytics", href: "/pnl" },
      { label: "Engine Reports", href: "/reports" },
      { label: "Evidence — Screener Reports", href: "/screener-reports" },
      { label: "Campaigns", href: "/campaigns" },
      { label: "Replay & Backtesting", href: "/replay" },
      { label: "AI Advisor", href: "/ai" },
    ],
    match: ["/pnl"],
  },
  {
    // Incident alerts stay distinct from rule configuration (refine
    // command §3): separate secondary links, never one merged label.
    id: "alerts",
    label: "Alerts & Rules",
    href: "/alerts",
    secondary: [
      { label: "Incident Alerts", href: "/alerts" },
      { label: "Screener Alert Rules", href: "/scanner-alerts" },
      { label: "Strategies", href: "/strategies" },
    ],
    match: ["/alerts", "/scanner-alerts", "/strategies"],
  },
  {
    // Own-context settings; platform administration is NOT here — it
    // lives in the role-gated Administration section below.
    id: "settings",
    label: "Settings",
    href: "/settings",
    secondary: [
      { label: "Account & Preferences", href: "/settings" },
      { label: "Organisation", href: "/org" },
      { label: "Billing", href: "/billing" },
      { label: "Telegram", href: "/telegram" },
    ],
    match: ["/settings", "/org", "/billing", "/telegram", "/onboarding"],
  },
];

// ADMIN_LINKS: explicitly-labelled operator administration (refine
// command §3: organisation administration is not platform
// administration). Rendered only for OPERATOR/ADMIN — and hidden while
// auth is still loading, so privileged navigation never flashes.
const ADMIN_LINKS: NavLink[] = [
  { label: "Risk Center", href: "/risk" },
  { label: "Exchanges", href: "/exchanges" },
  { label: "Markets", href: "/settings#markets" },
  { label: "System Health", href: "/system" },
  { label: "Audit Log", href: "/audit" },
];

// destinationFor: the destination a pathname belongs to — landing
// href first, then secondary hrefs, then the `match` prefixes (detail
// routes like /cycles/{id} activate their parent destination). Each
// route maps to exactly one destination by construction; usePathname
// never carries a hash, so /settings#markets matches "/settings".
function destinationFor(pathname: string): NavDestination | undefined {
  for (const d of DESTINATIONS) {
    if (d.href === pathname) return d;
  }
  for (const d of DESTINATIONS) {
    if (
      d.secondary.some((l) => (l.href.split("#")[0] ?? l.href) === pathname)
    )
      return d;
  }
  for (const d of DESTINATIONS) {
    if (d.match.some((m) => pathname === m || pathname.startsWith(m + "/")))
      return d;
  }
  return undefined;
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
// The former icon rail is gone (client-area audit §1: a group icon rail
// beside an independently scrolling label column was redundant chrome);
// the six destinations plus the active destination's contextual
// secondary links are the whole primary navigation.
function NavContent({
  pathname,
  role,
  onNavigate,
}: {
  pathname: string;
  role?: string;
  onNavigate?: () => void;
}) {
  const activeDest = destinationFor(pathname);
  // Auto-Paper is the UX spec's own package-gating example (console-v2.md
  // §2.1: "Auto-Paper 🔒Pro") — Watch's auto_paper.strategies is empty
  // (packages.md §2 "none (manual paper only)"), so an organisation on
  // Watch sees the nav item as package-gated rather than a plain link.
  // undefined (auth still loading, or anonymous) never gates optimistically.
  const autoPaperStrategies = useEntitlement("auto_paper.strategies");
  const autoPaperGated =
    autoPaperStrategies !== undefined && autoPaperStrategies.length === 0;
  // Administration is operator territory; a missing role (auth still
  // loading or anonymous) hides it rather than flashing it.
  const showAdmin = role === "OPERATOR" || role === "ADMIN";

  const secondaryLink = (item: NavLink, depthKey: string) => {
    const itemPath = item.href.split("#")[0] ?? item.href;
    const isActive = pathname === itemPath;
    if (item.label === "Auto-Paper rules" && autoPaperGated) {
      return (
        <GatedControl
          key={depthKey}
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
      <Link
        key={depthKey}
        href={item.href}
        onClick={onNavigate}
        aria-current={isActive ? "page" : undefined}
        className={`flex items-center gap-2 rounded px-2 py-1 ${
          isActive
            ? "bg-[var(--bg-raised)] font-medium text-[var(--text)]"
            : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
        }`}
      >
        <NavIcon label={item.label} />
        {item.label}
      </Link>
    );
  };

  return (
    <nav
      className="flex-1 overflow-y-auto text-[13px]"
      aria-label="Primary"
    >
      <div className="space-y-0.5">
        {DESTINATIONS.map((d) => {
          const isActive = activeDest?.id === d.id;
          return (
            <div key={d.id} data-nav-id={d.id}>
              <Link
                href={d.href}
                onClick={onNavigate}
                aria-current={isActive ? "page" : undefined}
                className={`flex items-center gap-2 rounded px-2 py-1.5 ${
                  isActive
                    ? "bg-[var(--bg-raised)] font-semibold text-[var(--text)]"
                    : "font-medium text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
                }`}
              >
                <NavIcon label={d.label} />
                {d.label}
              </Link>
              {isActive && d.secondary.length > 0 && (
                <div className="mt-0.5 mb-2 ml-4 space-y-0.5 border-l border-[var(--border)] pl-2">
                  {d.secondary.map((item) =>
                    secondaryLink(item, `${d.id}:${item.href}`),
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
      {showAdmin && (
        <div className="mt-4 border-t border-[var(--border)] pt-3">
          <div className="mb-1 px-2 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Administration
          </div>
          <div className="space-y-0.5">
            {ADMIN_LINKS.map((item) =>
              secondaryLink(item, `admin:${item.href}`),
            )}
          </div>
        </div>
      )}
    </nav>
  );
}

export function ConsoleShell({ children }: { children: ReactNode }) {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  // Route-based active state (client-area audit §4A): the current page
  // selects its destination from the pathname — pages no longer pass a
  // display-label `active` prop, so a renamed label can never break
  // selection.
  const pathname = usePathname();
  const [mobileOpen, setMobileOpen] = useState(false);
  // F15: Escape closes the mobile nav overlay, and focus returns to the
  // hamburger that opened it — the overlay was closable only by tapping
  // outside or navigating, and a keyboard user had no way out.
  useEffect(() => {
    if (!mobileOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMobileOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mobileOpen]);
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
              pathname={pathname}
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
        <NavContent pathname={pathname} role={role} />
      </aside>
      <main className="min-w-0 flex-1 p-4 md:p-6">
        <RestartBanner />
        {children}
      </main>
    </div>
  );
}
