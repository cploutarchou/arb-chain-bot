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
import {
  MOBILE_OVERLAY_QUERY,
  useFocusTrap,
  useMediaQuery,
} from "@/lib/a11y";
import { connectHub, type HubMessage } from "@/lib/ws";
import { useAuth, can } from "@/lib/auth";
import { Button, ConfirmDialog } from "@/components/ui";
import { MoonIcon, SunIcon } from "@/components/icons";
import {
  Breadcrumbs,
  PrimaryNav,
  SecondaryNav,
} from "@/components/ConsoleNav";
import { PaperControl } from "@/components/PaperControl";
import {
  NotificationBell,
  useNotificationItems,
} from "@/components/NotificationBell";

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

export function ConsoleShell({
  children,
  active,
}: {
  children: ReactNode;
  // active: retained only as a breadcrumb fallback for a route that
  // lib/nav.ts does not know. It is no longer how selection works —
  // `resolveNav(pathname)` decides that — so a page may omit it, and
  // renaming a label can no longer break a highlight. Three pages
  // passed a value matching no navigation label before this change
  // (/cycles/[id], /screener-reports/*, /onboarding) and highlighted
  // nothing at all; none of the 35 call sites had to change to fix that.
  active?: string;
}) {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const [mobileOpen, setMobileOpen] = useState(false);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const sheetRef = useRef<HTMLElement>(null);
  // The sheet only exists below md (its container is `md:hidden`), so
  // whenever it is open it is the full-screen-over-a-scrim presentation
  // and is modal. Containment is real here: without it Tab left the
  // sheet and walked the sidebar and page behind the scrim, which a
  // sighted keyboard user cannot see and a screen-reader user is not
  // told about.
  const mobileWidth = useMediaQuery(MOBILE_OVERLAY_QUERY);
  useFocusTrap(sheetRef, mobileOpen);
  // A sheet left open while the viewport grows past md would otherwise
  // become a focus trap inside a `display:none` subtree.
  useEffect(() => {
    if (mobileOpen && !mobileWidth) setMobileOpen(false);
  }, [mobileOpen, mobileWidth]);
  // Focus moves into the sheet on open. Without this the trap had
  // nothing to contain: focus stayed on the hamburger, which is outside
  // the sheet, so neither Tab boundary ever matched.
  useEffect(() => {
    if (mobileOpen) sheetRef.current?.focus();
  }, [mobileOpen]);
  // Escape closes the mobile nav overlay and focus returns to the
  // hamburger that opened it — a keyboard user otherwise had no way out.
  useEffect(() => {
    if (!mobileOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setMobileOpen(false);
        menuButtonRef.current?.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mobileOpen]);
  const modeState = useModeState();
  // Hoisted once for the whole shell: the mobile top bar, the mobile
  // overlay and the desktop sidebar each mount their own PaperControl
  // (CSS shows/hides them per breakpoint, all three are in the tree at
  // once) and must not each open a separate poll of the same endpoint.
  const paperStatus = usePoll<ScannerStatus>(() => api.scanner.status(), 5000);
  // One alerts+rule-events poll for the whole shell, shared by both bell
  // mounts — same reasoning as modeState feeding both ModeBanner mounts.
  const notificationItems = useNotificationItems();

  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      {/* Mobile top bar (< md): the hamburger reveals the same
          navigation definition as an overlay. The second row keeps the
          operating mode and the paper pause/resume control on every page
          at every width — neither waits for the menu to be opened. */}
      <div className="border-b border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 md:hidden">
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm font-semibold tracking-wide text-[var(--text)]">
            ARB CONSOLE
          </span>
          <div className="flex shrink-0 items-center gap-2">
            <NotificationBell items={notificationItems} />
            <ThemeToggle />
            <button
              ref={menuButtonRef}
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
        {/* flex-wrap, not a scroller: the mode and the pause control drop
            onto a second line on a narrow phone rather than requiring a
            horizontal scroll to reach the safety control. */}
        <div className="mt-2 flex flex-wrap items-center gap-2">
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
          {/* role/aria-modal belong on the sheet, not on the wrapper —
              the wrapper also contains the aria-hidden scrim. */}
          <aside
            ref={sheetRef}
            role="dialog"
            aria-modal="true"
            aria-label="Navigation"
            tabIndex={-1}
            className="relative z-50 flex w-72 max-w-[85vw] flex-col overflow-y-auto border-r border-[var(--border)] bg-[var(--bg-panel)] px-3 py-4 outline-none"
          >
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
            {/* One navigation definition serves both widths; the overlay
                always shows full labels. */}
            <PrimaryNav onNavigate={() => setMobileOpen(false)} />
            <SecondaryNav onNavigate={() => setMobileOpen(false)} />
          </aside>
        </div>
      )}

      <aside className="sticky top-0 hidden h-screen w-60 shrink-0 flex-col overflow-x-hidden border-r border-[var(--border)] bg-[var(--bg-panel)] px-3 py-4 md:flex">
        <div className="mb-2 flex items-center justify-between gap-1 px-2">
          <span className="min-w-0 truncate text-sm font-semibold tracking-wide text-[var(--text)]">
            ARB CONSOLE
          </span>
          <div className="flex shrink-0 items-center gap-1">
            <NotificationBell items={notificationItems} />
            <ThemeToggle />
          </div>
        </div>
        <div className="shrink-0 px-2">
          <ModeBanner
            status={modeState.status}
            configured={modeState.configured}
          />
          <PaperControl status={paperStatus} role={role} />
        </div>
        {/* PrimaryNav is shrink-0 and SecondaryNav owns the only scroll
            region, so the primary destinations are always visible
            without scrolling. The old sidebar scrolled the whole link
            column beside a second icon rail, which is what put later
            entries out of reach. */}
        <PrimaryNav />
        <SecondaryNav />
      </aside>
      <main className="min-w-0 flex-1 p-4 md:p-6">
        <RestartBanner />
        <Breadcrumbs fallbackLabel={active} />
        {children}
      </main>
    </div>
  );
}
