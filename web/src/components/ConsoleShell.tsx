"use client";

import Link from "next/link";
import { useEffect, useState, type ReactNode } from "react";
import { api, ApiError, type RecorderStatus, type SystemStatus } from "@/lib/api/client";
import { usePoll, type PollState } from "@/lib/usePoll";
import { connectHub, type HubMessage } from "@/lib/ws";
import { useAuth, can } from "@/lib/auth";
import { Button, ConfirmDialog } from "@/components/ui";

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

// useModeState hoists the RUNNING mode (REST, already polled once for
// both sidebar mounts — mobile overlay + desktop, T-057) and layers the
// hub "health" topic on top purely for the CONFIGURED mode (T-059 §2.3:
// {running, configured}), so the annotation degrades to nothing on a
// profile with no supervisor/engine rather than going permanently blank.
// One connectHub subscription for the whole shell, not one per mount.
function useModeState(): { status: PollState<SystemStatus>; configured: string | null } {
  const status = usePoll<SystemStatus>(() => api.system.status(), 10000);
  const [configured, setConfigured] = useState<string | null>(null);
  useEffect(() => {
    return connectHub(["health"], {
      onMessage: (msg: HubMessage) => {
        if (msg.topic !== "health") return;
        const data = msg.data as { mode?: { running?: string; configured?: string } } | undefined;
        if (data?.mode?.configured) setConfigured(data.mode.configured);
      },
    });
  }, []);
  return { status, configured };
}

function ModeBanner({ status, configured }: { status: PollState<SystemStatus>; configured: string | null }) {
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
  // T-059 §2.3: the banner shows the RUNNING mode and, when the platform-
  // settings document has been changed but not yet applied (restart
  // pending), annotates it with the CONFIGURED one.
  const showsPending = configured && configured !== mode;
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
      await api.engine.restart({ confirm: typed, reason: reason || undefined, stop_recording: stopRecording });
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
  if (restart.state === "ready" && (restart.pending_reasons?.length ?? 0) === 0) return null;

  const reasons = restart.pending_reasons?.join("; ") ?? "";
  const tone =
    restart.state === "failed" ? "var(--critical)" : restart.state === "restarting" ? "var(--warn)" : "var(--warn)";

  return (
    <div className="mb-3 rounded border px-3 py-2 text-[12px]" style={{ borderColor: tone, color: tone }}>
      {restart.state === "pending" && (
        <div className="flex flex-wrap items-center gap-2">
          <span>
            <strong>Saved but not running:</strong> {reasons}. Restarting reconnects the market-data
            feed, rebuilds the triangles, and starts a new paper session — persisted history is kept; a
            paused paper engine stays paused.
          </span>
          {mayRestart && (
            <Button onClick={openDialog} danger>
              Restart engine…
            </Button>
          )}
        </div>
      )}
      {restart.state === "restarting" && (
        <span>Restarting — waiting for the last recording segment to close and the feed to drain…</span>
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
              <Link href="/settings#platform-versions" className="text-[var(--accent)] underline">
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
                This cancels the running engine, waits for it to stop, and re-enters it with the current
                settings. In-memory paper balances reset to the configured starting balances; persisted
                opportunities, cycles and reports are kept. A paused paper engine stays paused.
              </p>
              {recorder?.running && (
                <label className="mb-3 flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={stopRecording}
                    onChange={(e) => setStopRecording(e.target.checked)}
                  />
                  Stop the active recording ({recorder.session_id ?? "unknown session"}) and restart
                </label>
              )}
              <label className="mb-1 block text-[12px] text-[var(--text-dim)]" htmlFor="restart-reason">
                Reason (optional)
              </label>
              <input
                id="restart-reason"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                className="mb-3 w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
              />
              <label className="mb-1 block text-[12px] text-[var(--text-dim)]" htmlFor="restart-confirm">
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
function NavContent({ active, role, onNavigate }: { active: string; role?: string; onNavigate?: () => void }) {
  return (
    <>
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
                      onClick={onNavigate}
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
        onClick={onNavigate}
        className={`mt-3 block rounded px-2 py-1 text-[13px] ${
          active === "Settings"
            ? "bg-[var(--bg-raised)] text-[var(--text)]"
            : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
        }`}
      >
        Settings
      </Link>
    </>
  );
}

export function ConsoleShell({ children, active }: { children: ReactNode; active: string }) {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const [mobileOpen, setMobileOpen] = useState(false);
  const modeState = useModeState();

  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      {/* Mobile top bar (< md): hamburger reveals the full nav as an
          overlay; the desktop sidebar below is hidden at this width. */}
      <div className="flex items-center justify-between border-b border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 md:hidden">
        <span className="text-sm font-semibold tracking-wide text-[var(--text)]">ARB CONSOLE</span>
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
      {mobileOpen && (
        <div className="fixed inset-0 z-40 flex md:hidden">
          <div className="absolute inset-0 bg-black/60" onClick={() => setMobileOpen(false)} aria-hidden />
          <aside className="relative z-50 flex w-72 max-w-[85vw] flex-col overflow-y-auto border-r border-[var(--border)] bg-[var(--bg-panel)] px-3 py-4">
            <div className="mb-2 px-2 text-sm font-semibold tracking-wide text-[var(--text)]">ARB CONSOLE</div>
            <div className="px-2">
              <ModeBanner status={modeState.status} configured={modeState.configured} />
            </div>
            <NavContent active={active} role={role} onNavigate={() => setMobileOpen(false)} />
          </aside>
        </div>
      )}

      <aside className="hidden w-56 shrink-0 flex-col border-r border-[var(--border)] bg-[var(--bg-panel)] px-3 py-4 md:flex">
        <div className="mb-2 px-2 text-sm font-semibold tracking-wide text-[var(--text)]">ARB CONSOLE</div>
        <div className="px-2">
          <ModeBanner status={modeState.status} configured={modeState.configured} />
        </div>
        <NavContent active={active} role={role} />
      </aside>
      <main className="min-w-0 flex-1 p-4 md:p-6">
        <RestartBanner />
        {children}
      </main>
    </div>
  );
}
