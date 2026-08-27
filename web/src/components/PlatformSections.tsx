"use client";

// Settings → Markets & assets / Venues & fees / (Telegram allowlist under
// Notifications) — the real editors for the versioned platform-settings
// document (T-057, docs/design/platform-settings-and-restart.md §4).
// Every write follows preview → ConfirmDialog(DiffTable) → apply; nothing
// here computes a financial number, a fee, or a triangle count — those
// come from `plan`/`diff`, both backend-supplied.

import { useState } from "react";
import {
  api,
  ApiError,
  isStaleVersion,
  staleVersion,
  type PlatformPreviewResponse,
  type PlatformSettingsDoc,
  type PlatformSnapshotView,
  type TelegramStatusView,
} from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { fmtDiffValue } from "@/lib/diff";
import {
  allowlistOf,
  clonePlatformSettings,
  effectForPath,
  enabledStartingAssets,
  normalizeToken,
  pruneOverrides,
  syncPaperBalances,
  timingLabel,
  updateAI,
  updatePlatform,
  updateTelegram,
  updateVenue,
} from "@/lib/platformFields";
import {
  Await,
  Badge,
  Button,
  ConfirmDialog,
  DiffTable,
  Section,
  StaleVersionNotice,
  Stat,
  Table,
  fmtTime,
} from "@/components/ui";

// ---- shared bits ------------------------------------------------------

function TimingChip({ effect }: { effect: "hot" | "restart" }) {
  return <Badge tone={effect === "hot" ? "dim" : "warn"}>{timingLabel(effect)}</Badge>;
}

interface PreviewDraft {
  draft: PlatformSettingsDoc;
  resp: PlatformPreviewResponse;
  // parentVersion (T-058): captured at startEdit — the version the
  // draft was actually cloned from — never re-read from a live poll at
  // review/apply time (usePoll can advance underneath an open editor).
  parentVersion: number;
}

function previewRows(preview: PlatformPreviewResponse, fieldTiming: Record<string, string>) {
  return Object.entries(preview.diff)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([path, ch]) => ({
      path,
      before: fmtDiffValue(ch.old),
      after: fmtDiffValue(ch.new),
      effect: timingLabel(effectForPath(fieldTiming, path)),
    }));
}

// modeConsequences (design §2.4/§6): the confirm dialog adds a
// mode-specific consequence line when the diff touches platform.mode —
// on TOP of the generic hot/restart copy, never instead of it. Text
// mirrors the design's wording; the actual guard rails (paper_enabled,
// recording_active) are enforced and worded by the backend at apply/
// restart time, this is purely informational.
function modeConsequences(diff: PlatformPreviewResponse["diff"]): string[] {
  const change = diff["platform.mode"];
  if (!change) return [];
  const out: string[] = [];
  const oldMode = typeof change.old === "string" ? change.old : String(change.old ?? "");
  const newMode = typeof change.new === "string" ? change.new : String(change.new ?? "");
  if (oldMode === "PAPER" && newMode !== "PAPER") {
    out.push(
      "Leaving PAPER closes the current paper session on the next restart; persisted cycles, orders and opportunities are kept, and in-memory balances reset.",
    );
  }
  if (newMode === "PAPER") {
    out.push(
      "Entering PAPER starts a new paper session on the next restart, funded with the configured starting balances; persisted history is kept.",
    );
  } else if (newMode === "RECORD") {
    out.push("Entering RECORD starts a new recording session on the next restart.");
  } else if (newMode === "MARKET_DATA") {
    out.push(
      "MARKET_DATA runs no simulation on the next restart — opportunities are still detected and logged, but no paper trades are placed.",
    );
  }
  return out;
}

// PlatformApplyDialog is the shared preview→confirm→apply modal both
// sections use — one component so the diff table / restart copy is
// identical everywhere the design's §3.3-style confirmation applies.
function PlatformApplyDialog({
  state,
  fieldTiming,
  onApplied,
  onCancel,
  onStale,
}: {
  state: PreviewDraft;
  fieldTiming: Record<string, string>;
  onApplied: (snap: PlatformSnapshotView) => void;
  onCancel: () => void;
  // onStale (T-058): a 409 stale_version fires this instead of setting
  // the inline `err` — the caller closes this dialog and shows the
  // page-level StaleVersionNotice/Reload affordance rather than letting
  // the operator retry Apply against a version that no longer exists.
  onStale: (currentVersion: number | null) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const rows = previewRows(state.resp, fieldTiming);
  const plans = state.resp.plan ? Object.values(state.resp.plan) : [];
  const consequences = modeConsequences(state.resp.diff);

  const confirm = async () => {
    setBusy(true);
    setErr("");
    try {
      const snap = await api.platform.apply(state.draft, state.parentVersion);
      onApplied(snap);
    } catch (e: unknown) {
      if (isStaleVersion(e)) {
        onStale(staleVersion(e));
        return;
      }
      setErr(e instanceof ApiError ? e.message : "Apply failed.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <ConfirmDialog
      title="Apply new platform settings?"
      confirmLabel={busy ? "Applying…" : "Apply"}
      confirmDisabled={busy}
      onConfirm={confirm}
      onCancel={onCancel}
      body={
        <>
          <p className="mb-3">
            {state.resp.requires_restart
              ? "This becomes a new platform settings version. Fields marked Immediate take effect right away; fields marked On restart are saved now but only take effect once the engine is restarted."
              : "This becomes a new platform settings version and takes effect immediately."}
          </p>
          {consequences.length > 0 && (
            <ul className="mb-3 list-inside list-disc space-y-1 text-[var(--warn)]">
              {consequences.map((c) => (
                <li key={c}>{c}</li>
              ))}
            </ul>
          )}
          <DiffTable rows={rows} beforeLabel="Current" afterLabel="New (draft)" showEffect />
          {plans.length > 0 && (
            <div className="mt-3 space-y-1 text-[12px] text-[var(--text-dim)]">
              {plans.map((p) => (
                <div key={p.venue}>
                  {p.venue}: {p.markets} markets → {p.triangles} triangles
                  {p.rejected_untradeable > 0 ? ` (${p.rejected_untradeable} rejected: untradeable)` : ""}
                </div>
              ))}
            </div>
          )}
          {err && <p className="mt-2 text-[var(--critical)]">{err}</p>}
        </>
      }
    />
  );
}

// appliedMessage renders warnings[] (settings-expansion §4.1) verbatim,
// appended to the version-applied confirmation — never reworded.
function appliedMessage(snap: PlatformSnapshotView): { ok: true; text: string } {
  const warn = snap.warnings?.length ? ` ${snap.warnings.join(" ")}` : "";
  return { ok: true, text: `Version ${snap.version} active.${warn}` };
}

async function runPreview(
  draft: PlatformSettingsDoc,
  parentVersion: number,
  setBusy: (b: boolean) => void,
  setErr: (s: string) => void,
  setState: (s: PreviewDraft | null) => void,
  setMsg: (m: { ok: boolean; text: string } | null) => void,
) {
  setErr("");
  setMsg(null);
  setBusy(true);
  try {
    const resp = await api.platform.preview(draft);
    if (Object.keys(resp.diff).length === 0) {
      setMsg({ ok: false, text: "No changes to apply." });
      return;
    }
    setState({ draft, resp, parentVersion });
  } catch (e: unknown) {
    setErr(e instanceof ApiError ? e.message : "Preview failed.");
  } finally {
    setBusy(false);
  }
}

// ---- Operating mode (T-059 §2, settings-expansion §6) --------------------
// platform.mode is restart-scoped: a change here is saved immediately as a
// new version, but only takes effect once the engine restarts (the
// ConsoleShell restart banner then offers Restart engine…, including the
// "stop the active recording" checkbox when one is running — T-057). LIVE
// has no entry in ModeTable at all; the permanent line beneath the picker
// says so.

export function OperatingModeSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayEdit = can(role, "system:config");

  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.platform.current(), 15000, [refresh]);
  const capabilities = usePoll(() => api.platform.capabilities(), 30000);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<PlatformSettingsDoc | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewErr, setPreviewErr] = useState("");
  const [previewState, setPreviewState] = useState<PreviewDraft | null>(null);
  // baseVersion/stale (T-058): the version startEdit cloned the draft
  // from, captured once and never re-read off the live poll while
  // editing; stale is set when apply comes back 409.
  const [baseVersion, setBaseVersion] = useState<number | null>(null);
  const [stale, setStale] = useState<{ current: number | null } | null>(null);

  const startEdit = (doc: PlatformSettingsDoc, version: number) => {
    setDraft(clonePlatformSettings(doc));
    setBaseVersion(version);
    setStale(null);
    setMsg(null);
    setPreviewErr("");
    setEditing(true);
  };
  const discard = () => {
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
  };
  const reloadAfterStale = () => {
    setStale(null);
    discard();
    setRefresh((n) => n + 1);
  };
  const setMode = (mode: string) => {
    setDraft((d) => (d ? updatePlatform(d, (p) => ({ ...p, mode })) : d));
  };

  const review = () => {
    if (!draft || baseVersion === null) return;
    void runPreview(draft, baseVersion, setPreviewBusy, setPreviewErr, setPreviewState, setMsg);
  };
  const applied = (snap: PlatformSnapshotView) => {
    setMsg(appliedMessage(snap));
    setStale(null);
    setPreviewState(null);
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
    setRefresh((n) => n + 1);
  };
  const onStale = (current: number | null) => {
    setStale({ current });
    setPreviewState(null);
  };

  return (
    <Section title="Operating mode">
      <p className="mb-3 max-w-2xl text-[13px] text-[var(--text-dim)]">
        The process-level mode this engine runs in. Changing it is restart-scoped — saved
        immediately as a new settings version, applied on the next engine restart.
      </p>
      {stale && <StaleVersionNotice currentVersion={stale.current} onReload={reloadAfterStale} />}
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      <Await state={current} what="platform settings">
        {(c) => {
          const doc = editing && draft ? draft : c.settings;
          const effect = effectForPath(c.field_timing, "platform.mode");
          return (
            <div className="max-w-2xl">
              <div className="mb-3 flex items-center gap-2">
                <Badge tone="ok">v{c.version}</Badge>
                <TimingChip effect={effect} />
                {mayEdit && !editing && <Button onClick={() => startEdit(c.settings, c.version)}>Edit mode</Button>}
                {editing && (
                  <>
                    <Button onClick={review} disabled={previewBusy}>
                      {previewBusy ? "Checking…" : "Review changes"}
                    </Button>
                    <Button onClick={discard} danger>
                      Discard draft
                    </Button>
                  </>
                )}
              </div>
              {!mayEdit && <p className="mb-2 text-[12px] text-[var(--text-dim)]">Requires ADMIN (system:config).</p>}
              {previewErr && <p className="mb-2 text-[12px] text-[var(--critical)]">{previewErr}</p>}

              <Await state={capabilities} what="mode capabilities">
                {(caps) => (
                  <div className="space-y-2">
                    {caps.modes.map((m) => (
                      <label
                        key={m.id}
                        className={`flex items-start gap-2 rounded border border-[var(--border)] p-2 text-[13px] ${
                          m.available ? "" : "opacity-60"
                        }`}
                      >
                        <input
                          type="radio"
                          name="platform-mode"
                          aria-label={m.id}
                          value={m.id}
                          className="mt-0.5"
                          checked={doc.platform.mode === m.id}
                          disabled={!editing || !mayEdit || !m.available}
                          onChange={() => setMode(m.id)}
                        />
                        <span>
                          <span className="font-medium">{m.id}</span>
                          {!m.available && (
                            <span className="ml-2 text-[12px] text-[var(--warn)]">Unavailable — {m.reason}</span>
                          )}
                          {m.id === "MARKET_DATA" && (
                            <span className="ml-2 text-[12px] text-[var(--text-dim)]">
                              market data only — no simulation runs, opportunities are still detected
                            </span>
                          )}
                          {m.id === "RECORD" && (
                            <span className="ml-2 text-[12px] text-[var(--text-dim)]">
                              records public market data; no orders placed
                            </span>
                          )}
                          {m.id === "PAPER" && (
                            <span className="ml-2 text-[12px] text-[var(--text-dim)]">
                              simulates fills against real books with virtual balances
                            </span>
                          )}
                        </span>
                      </label>
                    ))}
                  </div>
                )}
              </Await>
              <p className="mt-3 text-[12px] text-[var(--text-dim)]">
                LIVE is not an option: this platform never places real orders.
              </p>
            </div>
          );
        }}
      </Await>
      {previewState && current.kind === "ready" && (
        <PlatformApplyDialog
          state={previewState}
          fieldTiming={current.data.field_timing}
          onApplied={applied}
          onStale={onStale}
          onCancel={() => setPreviewState(null)}
        />
      )}
    </Section>
  );
}

// ---- Logging & access (T-059 §4.3) ---------------------------------------
// Both fields are hot: platform.log_level swaps a package-scoped
// slog.LevelVar; platform.allowed_origin swaps an atomic accessor the
// websocket origin check reads. Neither needs a restart.

export function LoggingAccessSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayEdit = can(role, "system:config");

  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.platform.current(), 15000, [refresh]);
  const capabilities = usePoll(() => api.platform.capabilities(), 30000);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<PlatformSettingsDoc | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewErr, setPreviewErr] = useState("");
  const [previewState, setPreviewState] = useState<PreviewDraft | null>(null);
  const [baseVersion, setBaseVersion] = useState<number | null>(null);
  const [stale, setStale] = useState<{ current: number | null } | null>(null);

  const startEdit = (doc: PlatformSettingsDoc, version: number) => {
    setDraft(clonePlatformSettings(doc));
    setBaseVersion(version);
    setStale(null);
    setMsg(null);
    setPreviewErr("");
    setEditing(true);
  };
  const discard = () => {
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
  };
  const reloadAfterStale = () => {
    setStale(null);
    discard();
    setRefresh((n) => n + 1);
  };
  const setLogLevel = (log_level: string) => {
    setDraft((d) => (d ? updatePlatform(d, (p) => ({ ...p, log_level })) : d));
  };
  const setOrigin = (allowed_origin: string) => {
    setDraft((d) => (d ? updatePlatform(d, (p) => ({ ...p, allowed_origin })) : d));
  };

  const review = () => {
    if (!draft || baseVersion === null) return;
    void runPreview(draft, baseVersion, setPreviewBusy, setPreviewErr, setPreviewState, setMsg);
  };
  const applied = (snap: PlatformSnapshotView) => {
    setMsg(appliedMessage(snap));
    setStale(null);
    setPreviewState(null);
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
    setRefresh((n) => n + 1);
  };
  const onStale = (current: number | null) => {
    setStale({ current });
    setPreviewState(null);
  };

  const logLevels = capabilities.kind === "ready" ? capabilities.data.log_levels : [];

  return (
    <Section title="Logging & access">
      {stale && <StaleVersionNotice currentVersion={stale.current} onReload={reloadAfterStale} />}
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      <Await state={current} what="platform settings">
        {(c) => {
          const doc = editing && draft ? draft : c.settings;
          const logEffect = effectForPath(c.field_timing, "platform.log_level");
          const originEffect = effectForPath(c.field_timing, "platform.allowed_origin");
          return (
            <div className="max-w-xl space-y-3">
              <div className="flex items-center gap-2">
                <Badge tone="ok">v{c.version}</Badge>
                {mayEdit && !editing && (
                  <Button onClick={() => startEdit(c.settings, c.version)}>Edit logging & access</Button>
                )}
                {editing && (
                  <>
                    <Button onClick={review} disabled={previewBusy}>
                      {previewBusy ? "Checking…" : "Review changes"}
                    </Button>
                    <Button onClick={discard} danger>
                      Discard draft
                    </Button>
                  </>
                )}
              </div>
              {!mayEdit && <p className="text-[12px] text-[var(--text-dim)]">Requires ADMIN (system:config).</p>}
              {previewErr && <p className="text-[12px] text-[var(--critical)]">{previewErr}</p>}

              <div>
                <label className="mb-1 flex items-center gap-2 text-[12px] text-[var(--text-dim)]" htmlFor="log-level">
                  Log level <TimingChip effect={logEffect} />
                </label>
                <select
                  id="log-level"
                  value={doc.platform.log_level}
                  disabled={!editing || !mayEdit}
                  onChange={(e) => setLogLevel(e.target.value)}
                  className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                >
                  {(logLevels.length > 0 ? logLevels : [doc.platform.log_level]).map((lvl) => (
                    <option key={lvl} value={lvl}>
                      {lvl}
                    </option>
                  ))}
                </select>
                <p className="mt-1 text-[11px] text-[var(--warn)]">
                  debug logs every rejected opportunity — a busy scanning period can produce thousands
                  of records; use it for troubleshooting only.
                </p>
              </div>

              <div>
                <label className="mb-1 flex items-center gap-2 text-[12px] text-[var(--text-dim)]" htmlFor="allowed-origin">
                  Allowed origin <TimingChip effect={originEffect} />
                </label>
                <input
                  id="allowed-origin"
                  value={doc.platform.allowed_origin}
                  disabled={!editing || !mayEdit}
                  onChange={(e) => setOrigin(e.target.value)}
                  placeholder="http://localhost:3000"
                  spellCheck={false}
                  className="w-72 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                />
                <p className="mt-1 text-[11px] text-[var(--text-dim)]">
                  scheme://host[:port] — the origin the websocket and CORS checks accept. A same-origin
                  console request is never blocked by a bad value here.
                </p>
              </div>
            </div>
          );
        }}
      </Await>
      {previewState && current.kind === "ready" && (
        <PlatformApplyDialog
          state={previewState}
          fieldTiming={current.data.field_timing}
          onApplied={applied}
          onStale={onStale}
          onCancel={() => setPreviewState(null)}
        />
      )}
    </Section>
  );
}

// ---- Markets & assets (BL-13b) ----------------------------------------

export function MarketsSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayEditVenues = can(role, "exchange:config");
  const mayEditPaper = can(role, "system:config");

  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.platform.current(), 15000, [refresh]);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<PlatformSettingsDoc | null>(null);
  const [symbolInput, setSymbolInput] = useState<Record<string, string>>({});
  const [assetInput, setAssetInput] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewErr, setPreviewErr] = useState("");
  const [previewState, setPreviewState] = useState<PreviewDraft | null>(null);
  const [baseVersion, setBaseVersion] = useState<number | null>(null);
  const [stale, setStale] = useState<{ current: number | null } | null>(null);

  // startEdit clones the doc as-is without a syncPaperBalances pass: the
  // ACTIVE snapshot is always already set-equality-consistent (the backend
  // rejects anything that isn't), so the clone starts consistent too.
  // addStartingAsset/removeStartingAsset call syncPaperBalances themselves
  // on every structural edit — this is the one invariant a future editor
  // must preserve if this section gains another way to change the
  // enabled/starting-asset union.
  const startEdit = (doc: PlatformSettingsDoc, version: number) => {
    setDraft(clonePlatformSettings(doc));
    setBaseVersion(version);
    setStale(null);
    setSymbolInput({});
    setAssetInput({});
    setMsg(null);
    setPreviewErr("");
    setEditing(true);
  };
  const discard = () => {
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
  };
  const reloadAfterStale = () => {
    setStale(null);
    discard();
    setRefresh((n) => n + 1);
  };

  const addSymbol = (venueId: string) => {
    const sym = normalizeToken(symbolInput[venueId] ?? "");
    if (!sym) return;
    setDraft((d) =>
      d ? updateVenue(d, venueId, (v) => (v.symbols.includes(sym) ? v : { ...v, symbols: [...v.symbols, sym].sort() })) : d,
    );
    setSymbolInput((prev) => ({ ...prev, [venueId]: "" }));
  };
  const removeSymbol = (venueId: string, sym: string) => {
    setDraft((d) => {
      if (!d) return d;
      const removed = updateVenue(d, venueId, (v) => ({ ...v, symbols: v.symbols.filter((s) => s !== sym) }));
      return updateVenue(removed, venueId, (v) => pruneOverrides(v));
    });
  };
  const addStartingAsset = (venueId: string) => {
    const a = normalizeToken(assetInput[venueId] ?? "");
    if (!a) return;
    setDraft((d) => {
      if (!d) return d;
      const next = updateVenue(d, venueId, (v) =>
        v.starting_assets.includes(a) ? v : { ...v, starting_assets: [...v.starting_assets, a].sort() },
      );
      return syncPaperBalances(next);
    });
    setAssetInput((prev) => ({ ...prev, [venueId]: "" }));
  };
  const removeStartingAsset = (venueId: string, a: string) => {
    setDraft((d) => {
      if (!d) return d;
      const next = updateVenue(d, venueId, (v) => ({ ...v, starting_assets: v.starting_assets.filter((x) => x !== a) }));
      return syncPaperBalances(next);
    });
  };
  const setBalance = (asset: string, value: string) => {
    setDraft((d) => (d ? { ...d, paper: { balances: { ...d.paper.balances, [asset]: value } } } : d));
  };

  const review = () => {
    if (!draft || baseVersion === null) return;
    void runPreview(draft, baseVersion, setPreviewBusy, setPreviewErr, setPreviewState, setMsg);
  };

  const applied = (snap: PlatformSnapshotView) => {
    setMsg(appliedMessage(snap));
    setStale(null);
    setPreviewState(null);
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
    setRefresh((n) => n + 1);
  };
  const onStale = (current: number | null) => {
    setStale({ current });
    setPreviewState(null);
  };

  return (
    <Section title="Markets & assets">
      <p className="mb-3 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Symbols, starting assets and their paper balances are part of the versioned platform-settings
        document. Every field here applies on restart — the timing chip is always what the backend
        reports, not assumed.
      </p>
      {stale && <StaleVersionNotice currentVersion={stale.current} onReload={reloadAfterStale} />}
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      <Await state={current} what="platform settings">
        {(c) => {
          const doc = editing && draft ? draft : c.settings;
          const balanceAssets = editing ? enabledStartingAssets(doc) : Object.keys(c.settings.paper.balances).sort();
          return (
            <div className="max-w-3xl space-y-4">
              <div className="flex items-center gap-2">
                <Badge tone="ok">v{c.version}</Badge>
                {mayEditVenues && !editing && (
                  <Button onClick={() => startEdit(c.settings, c.version)}>Edit markets & assets</Button>
                )}
                {editing && (
                  <>
                    <Button onClick={review} disabled={previewBusy}>
                      {previewBusy ? "Checking…" : "Review changes"}
                    </Button>
                    <Button onClick={discard} danger>
                      Discard draft
                    </Button>
                  </>
                )}
              </div>
              {!mayEditVenues && (
                <p className="text-[12px] text-[var(--text-dim)]">Editing symbols requires ADMIN (exchange:config).</p>
              )}
              {previewErr && <p className="text-[12px] text-[var(--critical)]">{previewErr}</p>}

              {Object.entries(doc.venues).map(([venueId, v]) => {
                const plan = c.plan?.[venueId];
                return (
                  <div key={venueId} className="rounded border border-[var(--border)] p-3">
                    <div className="mb-2 flex items-center justify-between">
                      <span className="text-[13px] font-semibold">{venueId}</span>
                      <TimingChip effect={effectForPath(c.field_timing, `venues.${venueId}.symbols`)} />
                    </div>
                    {plan && (
                      <p className="mb-2 text-[12px] text-[var(--text-dim)]">
                        {plan.markets} markets → {plan.triangles} triangles
                        {plan.rejected_untradeable > 0 ? ` (${plan.rejected_untradeable} rejected: untradeable)` : ""}
                      </p>
                    )}
                    <div className="mb-2">
                      <div className="mb-1 text-[12px] text-[var(--text-dim)]">Symbols</div>
                      <div className="flex flex-wrap gap-1">
                        {v.symbols.length === 0 && <span className="text-[12px] text-[var(--text-dim)]">none</span>}
                        {v.symbols.map((s) => (
                          <Badge key={s} tone="dim">
                            {s}
                            {editing && mayEditVenues && (
                              <button
                                type="button"
                                aria-label={`Remove symbol ${s}`}
                                onClick={() => removeSymbol(venueId, s)}
                                className="ml-1 text-[var(--critical)]"
                              >
                                ×
                              </button>
                            )}
                          </Badge>
                        ))}
                      </div>
                      {editing && mayEditVenues && (
                        <div className="mt-1 flex gap-1">
                          <input
                            aria-label={`Add symbol to ${venueId}`}
                            value={symbolInput[venueId] ?? ""}
                            onChange={(e) => setSymbolInput((p) => ({ ...p, [venueId]: e.target.value }))}
                            onKeyDown={(e) => e.key === "Enter" && addSymbol(venueId)}
                            placeholder="BTCUSDT"
                            className="w-32 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-0.5 text-[12px] outline-none focus:border-[var(--accent)]"
                          />
                          <Button onClick={() => addSymbol(venueId)}>Add</Button>
                        </div>
                      )}
                    </div>
                    <div>
                      <div className="mb-1 text-[12px] text-[var(--text-dim)]">Starting assets</div>
                      <div className="flex flex-wrap gap-1">
                        {v.starting_assets.length === 0 && <span className="text-[12px] text-[var(--text-dim)]">none</span>}
                        {v.starting_assets.map((a) => (
                          <Badge key={a} tone="dim">
                            {a}
                            {editing && mayEditVenues && (
                              <button
                                type="button"
                                aria-label={`Remove starting asset ${a}`}
                                onClick={() => removeStartingAsset(venueId, a)}
                                className="ml-1 text-[var(--critical)]"
                              >
                                ×
                              </button>
                            )}
                          </Badge>
                        ))}
                      </div>
                      {editing && mayEditVenues && (
                        <div className="mt-1 flex gap-1">
                          <input
                            aria-label={`Add starting asset to ${venueId}`}
                            value={assetInput[venueId] ?? ""}
                            onChange={(e) => setAssetInput((p) => ({ ...p, [venueId]: e.target.value }))}
                            onKeyDown={(e) => e.key === "Enter" && addStartingAsset(venueId)}
                            placeholder="USDT"
                            className="w-32 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-0.5 text-[12px] outline-none focus:border-[var(--accent)]"
                          />
                          <Button onClick={() => addStartingAsset(venueId)}>Add</Button>
                        </div>
                      )}
                    </div>
                  </div>
                );
              })}

              <div className="rounded border border-[var(--border)] p-3">
                <div className="mb-2 flex items-center justify-between">
                  <span className="text-[13px] font-semibold">Paper starting balances</span>
                </div>
                {!mayEditPaper && (
                  <p className="mb-2 text-[12px] text-[var(--text-dim)]">Editing balances requires ADMIN (system:config).</p>
                )}
                {balanceAssets.length === 0 ? (
                  <p className="text-[13px] text-[var(--text-dim)]">No starting assets configured yet.</p>
                ) : (
                  <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                    {balanceAssets.map((asset) => (
                      <label key={asset} className="flex items-center gap-2 text-[12px]">
                        <span className="w-14 shrink-0 text-[var(--text-dim)]">{asset}</span>
                        <input
                          value={editing ? draft?.paper.balances[asset] ?? "" : c.settings.paper.balances[asset] ?? ""}
                          disabled={!editing || !mayEditPaper}
                          onChange={(e) => setBalance(asset, e.target.value)}
                          inputMode="decimal"
                          className="w-full min-w-0 flex-1 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-0.5 outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                        />
                        {/* Per-asset chip, not a section-level guess — field_timing only
                            ever has "paper.balances.{asset}" keys (settings.go:373), never
                            a bare "paper.balances". */}
                        <TimingChip effect={effectForPath(c.field_timing, `paper.balances.${asset}`)} />
                      </label>
                    ))}
                  </div>
                )}
              </div>
            </div>
          );
        }}
      </Await>
      {previewState && current.kind === "ready" && (
        <PlatformApplyDialog
          state={previewState}
          fieldTiming={current.data.field_timing}
          onApplied={applied}
          onStale={onStale}
          onCancel={() => setPreviewState(null)}
        />
      )}
    </Section>
  );
}

// ---- Venues & fees (BL-15) ----------------------------------------------

export function VenuesSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayEdit = can(role, "exchange:config");

  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.platform.current(), 15000, [refresh]);
  const capabilities = usePoll(() => api.platform.capabilities(), 30000);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<PlatformSettingsDoc | null>(null);
  const [ovSymbol, setOvSymbol] = useState<Record<string, string>>({});
  const [ovMaker, setOvMaker] = useState<Record<string, string>>({});
  const [ovTaker, setOvTaker] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewErr, setPreviewErr] = useState("");
  const [previewState, setPreviewState] = useState<PreviewDraft | null>(null);
  const [baseVersion, setBaseVersion] = useState<number | null>(null);
  const [stale, setStale] = useState<{ current: number | null } | null>(null);

  // startEdit clones the doc as-is: the ACTIVE snapshot always already
  // satisfies paper.balances' set-equality invariant. toggleEnabled is the
  // only control here that changes the enabled-venue union, and it calls
  // syncPaperBalances itself — a future control that adds/removes
  // starting_assets in this section would need the same call.
  const startEdit = (doc: PlatformSettingsDoc, version: number) => {
    setDraft(clonePlatformSettings(doc));
    setBaseVersion(version);
    setStale(null);
    setMsg(null);
    setPreviewErr("");
    setEditing(true);
  };
  const discard = () => {
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
  };
  const reloadAfterStale = () => {
    setStale(null);
    discard();
    setRefresh((n) => n + 1);
  };

  const toggleEnabled = (venueId: string) => {
    setDraft((d) => (d ? syncPaperBalances(updateVenue(d, venueId, (v) => ({ ...v, enabled: !v.enabled }))) : d));
  };
  const togglePaperEnabled = (venueId: string) => {
    setDraft((d) => (d ? updateVenue(d, venueId, (v) => ({ ...v, paper_enabled: !v.paper_enabled })) : d));
  };
  // token_discount is never settable to true (backend rejects it
  // unconditionally: fees.go's rate/pay-asset apply without a
  // corresponding pay-asset ledger debit anywhere — settings.go's
  // FeeSettings.validate). The checkbox stays permanently disabled;
  // Discount below renders the compiled-in profile as read-only info.
  const setBps = (venueId: string, field: "maker_bps" | "taker_bps", value: string) => {
    setDraft((d) => (d ? updateVenue(d, venueId, (v) => ({ ...v, fees: { ...v.fees, [field]: value } })) : d));
  };
  const addOverride = (venueId: string) => {
    const sym = normalizeToken(ovSymbol[venueId] ?? "");
    const maker = (ovMaker[venueId] ?? "").trim();
    const taker = (ovTaker[venueId] ?? "").trim();
    if (!sym || !maker || !taker) return;
    setDraft((d) =>
      d
        ? updateVenue(d, venueId, (v) => ({
            ...v,
            fees: { ...v.fees, overrides: { ...v.fees.overrides, [sym]: { maker_bps: maker, taker_bps: taker } } },
          }))
        : d,
    );
    setOvSymbol((p) => ({ ...p, [venueId]: "" }));
    setOvMaker((p) => ({ ...p, [venueId]: "" }));
    setOvTaker((p) => ({ ...p, [venueId]: "" }));
  };
  const removeOverride = (venueId: string, sym: string) => {
    setDraft((d) =>
      d
        ? updateVenue(d, venueId, (v) => {
            const overrides = { ...v.fees.overrides };
            delete overrides[sym];
            return { ...v, fees: { ...v.fees, overrides: Object.keys(overrides).length ? overrides : undefined } };
          })
        : d,
    );
  };

  const review = () => {
    if (!draft || baseVersion === null) return;
    void runPreview(draft, baseVersion, setPreviewBusy, setPreviewErr, setPreviewState, setMsg);
  };
  const applied = (snap: PlatformSnapshotView) => {
    setMsg(appliedMessage(snap));
    setStale(null);
    setPreviewState(null);
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
    setRefresh((n) => n + 1);
  };
  const onStale = (current: number | null) => {
    setStale({ current });
    setPreviewState(null);
  };

  return (
    <Section title="Venues & fees">
      <p className="mb-3 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Every venue this build knows about — compiled-in and not. Per-venue enablement and fee
        tiers apply on restart. Public feed health is on the{" "}
        <a href="/exchanges" className="text-[var(--accent)] underline">
          Exchanges
        </a>{" "}
        page.
      </p>
      {stale && <StaleVersionNotice currentVersion={stale.current} onReload={reloadAfterStale} />}
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      <Await state={current} what="platform settings">
        {(c) => {
          const doc = editing && draft ? draft : c.settings;
          return (
            <div className="max-w-3xl space-y-4">
              <div className="flex items-center gap-2">
                <Badge tone="ok">v{c.version}</Badge>
                {mayEdit && !editing && (
                  <Button onClick={() => startEdit(c.settings, c.version)}>Edit venues & fees</Button>
                )}
                {editing && (
                  <>
                    <Button onClick={review} disabled={previewBusy}>
                      {previewBusy ? "Checking…" : "Review changes"}
                    </Button>
                    <Button onClick={discard} danger>
                      Discard draft
                    </Button>
                  </>
                )}
              </div>
              {!mayEdit && <p className="text-[12px] text-[var(--text-dim)]">Requires ADMIN (exchange:config).</p>}
              {previewErr && <p className="text-[12px] text-[var(--critical)]">{previewErr}</p>}

              <Await state={capabilities} what="venue capabilities">
                {(caps) =>
                  caps.venues.map((vp) => {
                    const v = doc.venues[vp.id];
                    if (!vp.available || !v) {
                      return (
                        <div key={vp.id} className="rounded border border-[var(--border)] p-3 opacity-80">
                          <div className="mb-2 flex items-center justify-between">
                            <span className="text-[13px] font-semibold">{vp.name}</span>
                            <Badge tone={vp.available ? "warn" : "dim"}>
                              {vp.available ? "Not configured" : "Not available"}
                            </Badge>
                          </div>
                          <p className="mb-2 text-[12px] text-[var(--text-dim)]">
                            {vp.available
                              ? "Compiled into this build but not yet in the current settings document."
                              : vp.reason}
                          </p>
                          {vp.discount && <DiscountInfo discount={vp.discount} />}
                          <p className="mt-2 text-[11px] text-[var(--text-dim)]">
                            Public market data only — no API keys are used or accepted.
                          </p>
                        </div>
                      );
                    }
                    const venueId = vp.id;
                    return (
                      <div key={venueId} className="rounded border border-[var(--border)] p-3">
                        <div className="mb-2 flex items-center justify-between">
                          <span className="text-[13px] font-semibold">{vp.name}</span>
                          <TimingChip effect={effectForPath(c.field_timing, `venues.${venueId}.enabled`)} />
                        </div>
                        <div className="mb-2 flex flex-wrap items-center gap-4 text-[12px]">
                          <label className="flex items-center gap-1.5">
                            <input
                              type="checkbox"
                              checked={v.enabled}
                              disabled={!editing || !mayEdit}
                              onChange={() => toggleEnabled(venueId)}
                            />
                            Enabled
                          </label>
                          <label className="flex items-center gap-1.5">
                            <input
                              type="checkbox"
                              checked={v.paper_enabled}
                              disabled={!editing || !mayEdit}
                              onChange={() => togglePaperEnabled(venueId)}
                            />
                            Paper enabled
                          </label>
                          <label className="flex items-center gap-1.5" title="Not supported: no pay-asset ledger">
                            <input type="checkbox" checked={v.fees.token_discount} disabled readOnly />
                            Token fee discount
                          </label>
                        </div>
                        {vp.discount ? (
                          <DiscountInfo discount={vp.discount} />
                        ) : (
                          <p className="mb-2 text-[11px] text-[var(--text-dim)]">
                            No compiled-in token-discount profile for this venue.
                          </p>
                        )}
                        <p className="mb-2 text-[11px] text-[var(--text-dim)]">
                          Public market data only — no API keys are used or accepted.
                        </p>
                        <div className="mb-2 flex gap-4">
                          <label className="flex items-center gap-2 text-[12px]">
                            <span className="w-20 text-[var(--text-dim)]">Maker (bps)</span>
                            <input
                              value={v.fees.maker_bps}
                              disabled={!editing || !mayEdit}
                              onChange={(e) => setBps(venueId, "maker_bps", e.target.value)}
                              inputMode="decimal"
                              className="w-24 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-0.5 outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                            />
                          </label>
                          <label className="flex items-center gap-2 text-[12px]">
                            <span className="w-20 text-[var(--text-dim)]">Taker (bps)</span>
                            <input
                              value={v.fees.taker_bps}
                              disabled={!editing || !mayEdit}
                              onChange={(e) => setBps(venueId, "taker_bps", e.target.value)}
                              inputMode="decimal"
                              className="w-24 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-0.5 outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                            />
                          </label>
                        </div>
                        <div>
                          <div className="mb-1 text-[12px] text-[var(--text-dim)]">Per-symbol overrides</div>
                          <Table
                            head={["Symbol", "Maker (bps)", "Taker (bps)", ""]}
                            empty="overrides"
                            rows={Object.entries(v.fees.overrides ?? {}).map(([sym, o]) => [
                              sym,
                              o.maker_bps,
                              o.taker_bps,
                              editing && mayEdit ? (
                                <Button key="rm" onClick={() => removeOverride(venueId, sym)} danger>
                                  Remove
                                </Button>
                              ) : (
                                ""
                              ),
                            ])}
                          />
                          {editing && mayEdit && (
                            <div className="mt-1 flex flex-wrap items-center gap-1">
                              <select
                                aria-label={`Override symbol for ${venueId}`}
                                value={ovSymbol[venueId] ?? ""}
                                onChange={(e) => setOvSymbol((p) => ({ ...p, [venueId]: e.target.value }))}
                                className="rounded border border-[var(--border)] bg-[var(--bg)] px-1.5 py-0.5 text-[12px] outline-none"
                              >
                                <option value="">symbol…</option>
                                {v.symbols.map((s) => (
                                  <option key={s} value={s}>
                                    {s}
                                  </option>
                                ))}
                              </select>
                              <input
                                aria-label={`Override maker bps for ${venueId}`}
                                placeholder="maker bps"
                                value={ovMaker[venueId] ?? ""}
                                onChange={(e) => setOvMaker((p) => ({ ...p, [venueId]: e.target.value }))}
                                className="w-20 rounded border border-[var(--border)] bg-[var(--bg)] px-1.5 py-0.5 text-[12px] outline-none"
                              />
                              <input
                                aria-label={`Override taker bps for ${venueId}`}
                                placeholder="taker bps"
                                value={ovTaker[venueId] ?? ""}
                                onChange={(e) => setOvTaker((p) => ({ ...p, [venueId]: e.target.value }))}
                                className="w-20 rounded border border-[var(--border)] bg-[var(--bg)] px-1.5 py-0.5 text-[12px] outline-none"
                              />
                              <Button onClick={() => addOverride(venueId)}>Add override</Button>
                            </div>
                          )}
                        </div>
                      </div>
                    );
                  })
                }
              </Await>
            </div>
          );
        }}
      </Await>
      {previewState && current.kind === "ready" && (
        <PlatformApplyDialog
          state={previewState}
          fieldTiming={current.data.field_timing}
          onApplied={applied}
          onStale={onStale}
          onCancel={() => setPreviewState(null)}
        />
      )}
    </Section>
  );
}

// DiscountInfo renders a venue's compiled-in token-discount profile as
// read-only data (pay asset / rate / API eligibility / whether it is
// actually modeled) — never an editable control; the reason string is
// the backend's, verbatim.
function DiscountInfo({ discount }: { discount: { pay_asset: string; rate: string; applies_to_api: boolean; modeled: boolean; reason: string } }) {
  return (
    <div className="mb-2 rounded border border-[var(--border)] bg-[var(--bg)] p-2 text-[11px] text-[var(--text-dim)]">
      <div>
        Compiled discount: pay in {discount.pay_asset}, rate {discount.rate}
        {discount.applies_to_api ? " (applies to API-executed trades)" : " (does not apply to API-executed trades)"}
      </div>
      <div className={discount.modeled ? undefined : "mt-1 text-[var(--warn)]"}>
        {discount.modeled ? "Modeled in paper P&L." : discount.reason}
      </div>
    </div>
  );
}

// ---- Telegram allowlist (part of Notifications, BL-12) -------------------

export function TelegramAllowlistSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayEdit = can(role, "system:config");

  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.platform.current(), 15000, [refresh]);
  const status = usePoll<TelegramStatusView>(() => api.telegram.status(), 15000, [refresh]);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<PlatformSettingsDoc | null>(null);
  const [idInput, setIdInput] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewErr, setPreviewErr] = useState("");
  const [previewState, setPreviewState] = useState<PreviewDraft | null>(null);
  const [baseVersion, setBaseVersion] = useState<number | null>(null);
  const [stale, setStale] = useState<{ current: number | null } | null>(null);

  const startEdit = (doc: PlatformSettingsDoc, version: number) => {
    setDraft(clonePlatformSettings(doc));
    setBaseVersion(version);
    setStale(null);
    setIdInput("");
    setMsg(null);
    setPreviewErr("");
    setEditing(true);
  };
  const discard = () => {
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
  };
  const reloadAfterStale = () => {
    setStale(null);
    discard();
    setRefresh((n) => n + 1);
  };
  const addId = () => {
    const n = Number(idInput.trim());
    if (!Number.isInteger(n) || n <= 0) return;
    setDraft((d) => {
      if (!d) return d;
      const list = allowlistOf(d);
      // updateTelegram spreads the EXISTING telegram section — a bare
      // `{ allowlist }` literal here would silently drop `disabled` from
      // the draft (see lib/platformFields.ts's updateTelegram doc).
      return list.includes(n) ? d : updateTelegram(d, (t) => ({ ...t, allowlist: [...list, n].sort((a, b) => a - b) }));
    });
    setIdInput("");
  };
  const removeId = (n: number) => {
    setDraft((d) => (d ? updateTelegram(d, (t) => ({ ...t, allowlist: allowlistOf(d).filter((x) => x !== n) })) : d));
  };
  const toggleDisabled = () => {
    setDraft((d) => (d ? updateTelegram(d, (t) => ({ ...t, disabled: !t.disabled })) : d));
  };

  const review = () => {
    if (!draft || baseVersion === null) return;
    void runPreview(draft, baseVersion, setPreviewBusy, setPreviewErr, setPreviewState, setMsg);
  };
  const applied = (snap: PlatformSnapshotView) => {
    setMsg(appliedMessage(snap));
    setStale(null);
    setPreviewState(null);
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
    setRefresh((n) => n + 1);
  };
  const onStale = (current: number | null) => {
    setStale({ current });
    setPreviewState(null);
  };

  return (
    <div className="mt-4 max-w-xl">
      <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
        Telegram
      </h3>
      {stale && <StaleVersionNotice currentVersion={stale.current} onReload={reloadAfterStale} />}
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      {status.kind === "ready" && (
        <p className="mb-2 text-[12px] text-[var(--text-dim)]">
          Bot: {status.data.enabled ? <Badge tone="ok">running</Badge> : <Badge tone="dim">not running</Badge>}
          {status.data.reason ? ` — ${status.data.reason}` : ""}
        </p>
      )}
      <Await state={current} what="platform settings">
        {(c) => {
          const doc = editing && draft ? draft : c.settings;
          const allowlist = allowlistOf(doc);
          const effect = effectForPath(c.field_timing, "telegram.allowlist");
          const disabledEffect = effectForPath(c.field_timing, "telegram.disabled");
          return (
            <>
              <div className="mb-2 flex items-center gap-2">
                <label className="flex items-center gap-1.5 text-[13px]">
                  <input
                    type="checkbox"
                    checked={!doc.telegram.disabled}
                    disabled={!editing || !mayEdit}
                    onChange={toggleDisabled}
                  />
                  Telegram notifications
                </label>
                <TimingChip effect={disabledEffect} />
              </div>
              <p className="mb-2 text-[11px] text-[var(--text-dim)]">
                Turning this off mutes bot commands and alert pushes together — the allowlist below is
                kept, not cleared.
              </p>
              <div className="mb-2 flex items-center gap-2">
                <span className="text-[12px] text-[var(--text-dim)]">Allowlist:</span>
                <TimingChip effect={effect} />
                {effect === "restart" && (
                  <span className="text-[11px] text-[var(--text-dim)]">
                    the bot was started without an allowlist; adding the first user needs a restart
                  </span>
                )}
              </div>
              <p className="mb-2 text-[11px] text-[var(--text-dim)]">
                The bot token is configured via the environment only — it is never shown or accepted
                here.
              </p>
              <div className="mb-2 flex flex-wrap gap-1">
                {allowlist.length === 0 && (
                  <span className="text-[13px] text-[var(--text-dim)]">No chat IDs allowlisted.</span>
                )}
                {allowlist.map((id) => (
                  <Badge key={id} tone="dim">
                    {id}
                    {editing && mayEdit && (
                      <button
                        type="button"
                        aria-label={`Remove chat id ${id}`}
                        onClick={() => removeId(id)}
                        className="ml-1 text-[var(--critical)]"
                      >
                        ×
                      </button>
                    )}
                  </Badge>
                ))}
              </div>
              <div className="flex items-center gap-2">
                {mayEdit && !editing && (
                  <Button onClick={() => startEdit(c.settings, c.version)}>Edit Telegram settings</Button>
                )}
                {editing && (
                  <>
                    <input
                      aria-label="Add Telegram chat id"
                      value={idInput}
                      onChange={(e) => setIdInput(e.target.value)}
                      onKeyDown={(e) => e.key === "Enter" && addId()}
                      placeholder="123456789"
                      inputMode="numeric"
                      className="w-40 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[12px] outline-none focus:border-[var(--accent)]"
                    />
                    <Button onClick={addId}>Add</Button>
                    <Button onClick={review} disabled={previewBusy}>
                      {previewBusy ? "Checking…" : "Review changes"}
                    </Button>
                    <Button onClick={discard} danger>
                      Discard draft
                    </Button>
                  </>
                )}
              </div>
              {!mayEdit && <p className="mt-2 text-[12px] text-[var(--text-dim)]">Requires ADMIN (system:config).</p>}
              {previewErr && <p className="mt-2 text-[12px] text-[var(--critical)]">{previewErr}</p>}
              {previewState && (
                <PlatformApplyDialog
                  state={previewState}
                  fieldTiming={c.field_timing}
                  onApplied={applied}
                  onStale={onStale}
                  onCancel={() => setPreviewState(null)}
                />
              )}
            </>
          );
        }}
      </Await>
    </div>
  );
}

// ---- AI advisor (T-059 §4.1) ----------------------------------------------
// Every ai.* field is hot: ai.Service/ai.Scheduler are always constructed
// behind an ai.Switch, so an enable/provider/schedule/budget change here
// takes effect without a restart. The advisor gets no route into any
// settings document beyond this one — recommendations stay
// strategy.Params-only and human-approved (see the AI Advisor page).

function AIStatusCard() {
  const status = usePoll(() => api.ai.status(), 15000);
  return (
    <Await state={status} what="AI advisor status">
      {(s) => (
        <div className="mb-3 grid max-w-2xl grid-cols-2 gap-3 sm:grid-cols-4">
          <Stat label="Enabled" value={s.enabled ? "yes" : "no"} tone={s.enabled ? "ok" : "dim"} />
          <Stat label="Running" value={s.running ? "yes" : "no"} tone={s.running ? "ok" : "warn"} />
          <Stat label="Provider" value={s.provider || "—"} />
          <Stat label="Model" value={s.model || "—"} />
          <Stat label="Key source" value={s.key_source || "—"} />
          <Stat label="Analyses today" value={`${s.analyses_today} of ${s.max_per_day}`} />
          <Stat label="Last analysis" value={s.last_analysis ? fmtTime(s.last_analysis) : "—"} />
          {s.reason && <Stat label="Reason" value={s.reason} tone="warn" />}
        </div>
      )}
    </Await>
  );
}

export function AIAdvisorSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayEdit = can(role, "system:config");

  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.platform.current(), 15000, [refresh]);
  const capabilities = usePoll(() => api.platform.capabilities(), 30000);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<PlatformSettingsDoc | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewErr, setPreviewErr] = useState("");
  const [previewState, setPreviewState] = useState<PreviewDraft | null>(null);
  const [baseVersion, setBaseVersion] = useState<number | null>(null);
  const [stale, setStale] = useState<{ current: number | null } | null>(null);

  const startEdit = (doc: PlatformSettingsDoc, version: number) => {
    setDraft(clonePlatformSettings(doc));
    setBaseVersion(version);
    setStale(null);
    setMsg(null);
    setPreviewErr("");
    setEditing(true);
  };
  const discard = () => {
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
  };
  const reloadAfterStale = () => {
    setStale(null);
    discard();
    setRefresh((n) => n + 1);
  };
  const setEnabled = (enabled: boolean) => {
    setDraft((d) => (d ? updateAI(d, (a) => ({ ...a, enabled })) : d));
  };
  const setProvider = (provider: string) => {
    setDraft((d) => (d ? updateAI(d, (a) => ({ ...a, provider })) : d));
  };
  const setModel = (model: string) => {
    setDraft((d) => (d ? updateAI(d, (a) => ({ ...a, model })) : d));
  };
  const setScheduleField = (field: keyof PlatformSettingsDoc["ai"]["schedule"], value: number) => {
    setDraft((d) => (d ? updateAI(d, (a) => ({ ...a, schedule: { ...a.schedule, [field]: value } })) : d));
  };
  const setBudgetField = (field: keyof PlatformSettingsDoc["ai"]["budget"], value: number) => {
    setDraft((d) => (d ? updateAI(d, (a) => ({ ...a, budget: { ...a.budget, [field]: value } })) : d));
  };

  const review = () => {
    if (!draft || baseVersion === null) return;
    void runPreview(draft, baseVersion, setPreviewBusy, setPreviewErr, setPreviewState, setMsg);
  };
  const applied = (snap: PlatformSnapshotView) => {
    setMsg(appliedMessage(snap));
    setStale(null);
    setPreviewState(null);
    setEditing(false);
    setDraft(null);
    setBaseVersion(null);
    setRefresh((n) => n + 1);
  };
  const onStale = (current: number | null) => {
    setStale({ current });
    setPreviewState(null);
  };

  return (
    <Section title="AI advisor">
      <p className="mb-3 max-w-2xl text-[13px] text-[var(--text-dim)]">
        The advisor only proposes; nothing here changes strategy behavior by itself — recommendations
        are approved on the{" "}
        <a href="/ai" className="text-[var(--accent)] underline">
          AI Advisor
        </a>{" "}
        page like a manual config edit.
      </p>
      <AIStatusCard />
      {stale && <StaleVersionNotice currentVersion={stale.current} onReload={reloadAfterStale} />}
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      <Await state={current} what="platform settings">
        {(c) => {
          const doc = editing && draft ? draft : c.settings;
          const effect = effectForPath(c.field_timing, "ai.enabled");
          return (
            <div className="max-w-2xl space-y-3">
              <div className="flex items-center gap-2">
                <Badge tone="ok">v{c.version}</Badge>
                <TimingChip effect={effect} />
                {mayEdit && !editing && (
                  <Button onClick={() => startEdit(c.settings, c.version)}>Edit AI advisor</Button>
                )}
                {editing && (
                  <>
                    <Button onClick={review} disabled={previewBusy}>
                      {previewBusy ? "Checking…" : "Review changes"}
                    </Button>
                    <Button onClick={discard} danger>
                      Discard draft
                    </Button>
                  </>
                )}
              </div>
              {!mayEdit && <p className="text-[12px] text-[var(--text-dim)]">Requires ADMIN (system:config).</p>}
              {previewErr && <p className="text-[12px] text-[var(--critical)]">{previewErr}</p>}
              {c.warnings && c.warnings.length > 0 && (
                <ul className="list-inside list-disc text-[12px] text-[var(--warn)]">
                  {c.warnings.map((w) => (
                    <li key={w}>{w}</li>
                  ))}
                </ul>
              )}

              <label className="flex items-center gap-1.5 text-[13px]">
                <input
                  type="checkbox"
                  checked={doc.ai.enabled}
                  disabled={!editing || !mayEdit}
                  onChange={() => setEnabled(!doc.ai.enabled)}
                />
                Enable the AI advisor
              </label>

              <Await state={capabilities} what="AI provider capabilities">
                {(caps) => (
                  <div>
                    <label className="mb-1 block text-[12px] text-[var(--text-dim)]" htmlFor="ai-provider">
                      Provider
                    </label>
                    <select
                      id="ai-provider"
                      value={doc.ai.provider}
                      disabled={!editing || !mayEdit}
                      onChange={(e) => setProvider(e.target.value)}
                      className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                    >
                      {caps.ai_providers.map((p) => (
                        <option key={p.id} value={p.id} disabled={!p.available}>
                          {p.available ? p.id : `${p.id} — ${p.reason}`}
                        </option>
                      ))}
                    </select>
                  </div>
                )}
              </Await>

              <div>
                <label className="mb-1 block text-[12px] text-[var(--text-dim)]" htmlFor="ai-model">
                  Model
                </label>
                <input
                  id="ai-model"
                  value={doc.ai.model}
                  disabled={!editing || !mayEdit}
                  onChange={(e) => setModel(e.target.value)}
                  spellCheck={false}
                  className="w-64 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                />
              </div>

              <div>
                <div className="mb-1 text-[12px] text-[var(--text-dim)]">Standing-analysis schedule (0 disables)</div>
                <div className="flex flex-wrap gap-4">
                  <label className="flex items-center gap-2 text-[12px]">
                    <span className="w-32 text-[var(--text-dim)]">Hourly (minutes)</span>
                    <input
                      type="number"
                      min={0}
                      value={doc.ai.schedule.hourly_minutes}
                      disabled={!editing || !mayEdit}
                      onChange={(e) => setScheduleField("hourly_minutes", Number(e.target.value))}
                      className="w-24 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-0.5 outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                    />
                  </label>
                  <label className="flex items-center gap-2 text-[12px]">
                    <span className="w-24 text-[var(--text-dim)]">Daily (hours)</span>
                    <input
                      type="number"
                      min={0}
                      value={doc.ai.schedule.daily_hours}
                      disabled={!editing || !mayEdit}
                      onChange={(e) => setScheduleField("daily_hours", Number(e.target.value))}
                      className="w-24 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-0.5 outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                    />
                  </label>
                  <label className="flex items-center gap-2 text-[12px]">
                    <span className="w-24 text-[var(--text-dim)]">Weekly (hours)</span>
                    <input
                      type="number"
                      min={0}
                      value={doc.ai.schedule.weekly_hours}
                      disabled={!editing || !mayEdit}
                      onChange={(e) => setScheduleField("weekly_hours", Number(e.target.value))}
                      className="w-24 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-0.5 outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                    />
                  </label>
                </div>
                <p className="mt-1 text-[11px] text-[var(--text-dim)]">
                  0 or 15..1440 minutes hourly; 0 or 1..168 hours daily; 0 or 24..720 hours weekly.
                </p>
              </div>

              <div>
                <div className="mb-1 text-[12px] text-[var(--text-dim)]">Budget</div>
                <div className="flex flex-wrap gap-4">
                  <label className="flex items-center gap-2 text-[12px]">
                    <span className="w-36 text-[var(--text-dim)]">Max analyses / day</span>
                    <input
                      type="number"
                      min={1}
                      max={96}
                      value={doc.ai.budget.max_analyses_per_day}
                      disabled={!editing || !mayEdit}
                      onChange={(e) => setBudgetField("max_analyses_per_day", Number(e.target.value))}
                      className="w-24 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-0.5 outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                    />
                  </label>
                  <label className="flex items-center gap-2 text-[12px]">
                    <span className="w-36 text-[var(--text-dim)]">Max output tokens</span>
                    <input
                      type="number"
                      min={256}
                      max={8192}
                      value={doc.ai.budget.max_output_tokens}
                      disabled={!editing || !mayEdit}
                      onChange={(e) => setBudgetField("max_output_tokens", Number(e.target.value))}
                      className="w-24 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-0.5 outline-none disabled:opacity-50 focus:border-[var(--accent)]"
                    />
                  </label>
                </div>
                <p className="mt-1 text-[11px] text-[var(--text-dim)]">
                  1..96 analyses/day (a per-process counter — resets on restart, not persisted); 256..8192
                  output tokens.
                </p>
              </div>
            </div>
          );
        }}
      </Await>
      {previewState && current.kind === "ready" && (
        <PlatformApplyDialog
          state={previewState}
          fieldTiming={current.data.field_timing}
          onApplied={applied}
          onStale={onStale}
          onCancel={() => setPreviewState(null)}
        />
      )}
    </Section>
  );
}

// ---- Platform settings version history (shared by both sections) -------

export function PlatformVersionHistorySection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayRollback = can(role, "exchange:config") || can(role, "system:config");

  const [refresh, setRefresh] = useState(0);
  const versions = usePoll(() => api.platform.versions(25), 15000, [refresh]);

  // rollbackConfirm.parentVersion (T-058) is the ACTIVE version at the
  // moment the operator opened this dialog — captured from the same
  // `list` render the "Roll back to" button came from, never re-read at
  // confirm time (the versions poll can advance while the dialog is
  // open).
  const [rollbackConfirm, setRollbackConfirm] = useState<{ version: number; parentVersion: number } | null>(null);
  const [rollbackLoading, setRollbackLoading] = useState<number | null>(null);
  const [rollbackErr, setRollbackErr] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [stale, setStale] = useState<{ current: number | null } | null>(null);

  const openRollback = async (version: number, parentVersion: number | null) => {
    setRollbackErr("");
    if (parentVersion === null) {
      setRollbackErr("Could not determine the active version — reload and try again.");
      return;
    }
    setRollbackLoading(version);
    try {
      await api.platform.version(version);
      setRollbackConfirm({ version, parentVersion });
    } catch (err: unknown) {
      setRollbackErr(err instanceof ApiError ? err.message : "Could not load that version.");
    } finally {
      setRollbackLoading(null);
    }
  };

  const confirmRollback = async () => {
    if (!rollbackConfirm) return;
    setBusy(true);
    setMsg(null);
    try {
      const snap = await api.platform.rollback(rollbackConfirm.version, rollbackConfirm.parentVersion);
      setMsg({ ok: true, text: `Rolled back as new version ${snap.version}.` });
      setStale(null);
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      if (isStaleVersion(err)) {
        setStale({ current: staleVersion(err) });
      } else {
        setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Rollback failed." });
      }
    } finally {
      setBusy(false);
      setRollbackConfirm(null);
    }
  };

  const reloadAfterStale = () => {
    setStale(null);
    setRefresh((n) => n + 1);
  };

  return (
    <Section title="Platform settings — version history">
      {stale && <StaleVersionNotice currentVersion={stale.current} onReload={reloadAfterStale} />}
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      {rollbackErr && <p className="mb-2 text-[12px] text-[var(--critical)]">{rollbackErr}</p>}
      <Await state={versions} what="platform settings versions">
        {(list) => {
          const activeVersion = (list ?? []).find((v) => v.active)?.version ?? null;
          return (
            <Table
              head={["Version", "Created", "By", "Parent", "Changed paths", ""]}
              empty="versions"
              rows={(list ?? []).map((v) => [
              v.active ? (
                <Badge key="a" tone="ok">
                  v{v.version} active
                </Badge>
              ) : (
                `v${v.version}`
              ),
              fmtTime(v.created_at),
              v.created_by || "system",
              v.parent_version ? `v${v.parent_version}` : "—",
              v.diff && Object.keys(v.diff).length > 0 ? (
                <div key="d" className="max-w-xs whitespace-normal break-words text-[12px]">
                  {Object.entries(v.diff).map(([path, change]) => (
                    <div key={path}>
                      <span className="text-[var(--text-dim)]">{path}:</span> {fmtDiffValue(change.old)} →{" "}
                      {fmtDiffValue(change.new)}
                    </div>
                  ))}
                </div>
              ) : (
                "—"
              ),
              !v.active && mayRollback ? (
                <Button
                  key="rb"
                  onClick={() => openRollback(v.version, activeVersion)}
                  disabled={rollbackLoading === v.version}
                >
                  {rollbackLoading === v.version ? "Loading…" : "Roll back to"}
                </Button>
              ) : (
                ""
              ),
            ])}
          />
          );
        }}
      </Await>
      {rollbackConfirm && (
        <ConfirmDialog
          title={`Roll back platform settings to v${rollbackConfirm.version}?`}
          confirmLabel={busy ? "Rolling back…" : `Roll back to v${rollbackConfirm.version}`}
          confirmDisabled={busy}
          onConfirm={confirmRollback}
          onCancel={() => setRollbackConfirm(null)}
          body={
            <p>
              This creates a new platform settings version with v{rollbackConfirm.version}&apos;s content.
              Restart-scoped fields only take effect after the next engine restart.
            </p>
          }
        />
      )}
    </Section>
  );
}
