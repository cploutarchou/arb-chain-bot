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
  type PlatformPreviewResponse,
  type PlatformSettingsDoc,
  type PlatformSnapshotView,
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
  updateVenue,
} from "@/lib/platformFields";
import { Await, Badge, Button, ConfirmDialog, DiffTable, Section, Table, fmtTime } from "@/components/ui";

// ---- shared bits ------------------------------------------------------

function TimingChip({ effect }: { effect: "hot" | "restart" }) {
  return <Badge tone={effect === "hot" ? "dim" : "warn"}>{timingLabel(effect)}</Badge>;
}

interface PreviewDraft {
  draft: PlatformSettingsDoc;
  resp: PlatformPreviewResponse;
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

// PlatformApplyDialog is the shared preview→confirm→apply modal both
// sections use — one component so the diff table / restart copy is
// identical everywhere the design's §3.3-style confirmation applies.
function PlatformApplyDialog({
  state,
  fieldTiming,
  onApplied,
  onCancel,
}: {
  state: PreviewDraft;
  fieldTiming: Record<string, string>;
  onApplied: (snap: PlatformSnapshotView) => void;
  onCancel: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const rows = previewRows(state.resp, fieldTiming);
  const plans = state.resp.plan ? Object.values(state.resp.plan) : [];

  const confirm = async () => {
    setBusy(true);
    setErr("");
    try {
      const snap = await api.platform.apply(state.draft);
      onApplied(snap);
    } catch (e: unknown) {
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

async function runPreview(
  draft: PlatformSettingsDoc,
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
    setState({ draft, resp });
  } catch (e: unknown) {
    setErr(e instanceof ApiError ? e.message : "Preview failed.");
  } finally {
    setBusy(false);
  }
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

  // startEdit clones the doc as-is without a syncPaperBalances pass: the
  // ACTIVE snapshot is always already set-equality-consistent (the backend
  // rejects anything that isn't), so the clone starts consistent too.
  // addStartingAsset/removeStartingAsset call syncPaperBalances themselves
  // on every structural edit — this is the one invariant a future editor
  // must preserve if this section gains another way to change the
  // enabled/starting-asset union.
  const startEdit = (doc: PlatformSettingsDoc) => {
    setDraft(clonePlatformSettings(doc));
    setSymbolInput({});
    setAssetInput({});
    setMsg(null);
    setPreviewErr("");
    setEditing(true);
  };
  const discard = () => {
    setEditing(false);
    setDraft(null);
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
    if (!draft) return;
    void runPreview(draft, setPreviewBusy, setPreviewErr, setPreviewState, setMsg);
  };

  const applied = (snap: PlatformSnapshotView) => {
    setMsg({ ok: true, text: `Version ${snap.version} active.` });
    setPreviewState(null);
    setEditing(false);
    setDraft(null);
    setRefresh((n) => n + 1);
  };

  return (
    <Section title="Markets & assets">
      <p className="mb-3 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Symbols, starting assets and their paper balances are part of the versioned platform-settings
        document. Every field here applies on restart — the timing chip is always what the backend
        reports, not assumed.
      </p>
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      <Await state={current} what="platform settings">
        {(c) => {
          const doc = editing && draft ? draft : c.settings;
          const balanceAssets = editing ? enabledStartingAssets(doc) : Object.keys(c.settings.paper.balances).sort();
          return (
            <div className="max-w-3xl space-y-4">
              <div className="flex items-center gap-2">
                <Badge tone="ok">v{c.version}</Badge>
                {mayEditVenues && !editing && <Button onClick={() => startEdit(c.settings)}>Edit markets & assets</Button>}
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

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<PlatformSettingsDoc | null>(null);
  const [ovSymbol, setOvSymbol] = useState<Record<string, string>>({});
  const [ovMaker, setOvMaker] = useState<Record<string, string>>({});
  const [ovTaker, setOvTaker] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewErr, setPreviewErr] = useState("");
  const [previewState, setPreviewState] = useState<PreviewDraft | null>(null);

  // startEdit clones the doc as-is: the ACTIVE snapshot always already
  // satisfies paper.balances' set-equality invariant. toggleEnabled is the
  // only control here that changes the enabled-venue union, and it calls
  // syncPaperBalances itself — a future control that adds/removes
  // starting_assets in this section would need the same call.
  const startEdit = (doc: PlatformSettingsDoc) => {
    setDraft(clonePlatformSettings(doc));
    setMsg(null);
    setPreviewErr("");
    setEditing(true);
  };
  const discard = () => {
    setEditing(false);
    setDraft(null);
  };

  const toggleEnabled = (venueId: string) => {
    setDraft((d) => (d ? syncPaperBalances(updateVenue(d, venueId, (v) => ({ ...v, enabled: !v.enabled }))) : d));
  };
  const togglePaperEnabled = (venueId: string) => {
    setDraft((d) => (d ? updateVenue(d, venueId, (v) => ({ ...v, paper_enabled: !v.paper_enabled })) : d));
  };
  const toggleDiscount = (venueId: string) => {
    setDraft((d) =>
      d ? updateVenue(d, venueId, (v) => ({ ...v, fees: { ...v.fees, token_discount: !v.fees.token_discount } })) : d,
    );
  };
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
    if (!draft) return;
    void runPreview(draft, setPreviewBusy, setPreviewErr, setPreviewState, setMsg);
  };
  const applied = (snap: PlatformSnapshotView) => {
    setMsg({ ok: true, text: `Version ${snap.version} active.` });
    setPreviewState(null);
    setEditing(false);
    setDraft(null);
    setRefresh((n) => n + 1);
  };

  return (
    <Section title="Venues & fees">
      <p className="mb-3 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Per-venue enablement, fee tiers and the token-fee discount toggle. Every field applies on
        restart. Public feed health is on the{" "}
        <a href="/exchanges" className="text-[var(--accent)] underline">
          Exchanges
        </a>{" "}
        page.
      </p>
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      <Await state={current} what="platform settings">
        {(c) => {
          const doc = editing && draft ? draft : c.settings;
          return (
            <div className="max-w-3xl space-y-4">
              <div className="flex items-center gap-2">
                <Badge tone="ok">v{c.version}</Badge>
                {mayEdit && !editing && <Button onClick={() => startEdit(c.settings)}>Edit venues & fees</Button>}
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

              {Object.entries(doc.venues).map(([venueId, v]) => (
                <div key={venueId} className="rounded border border-[var(--border)] p-3">
                  <div className="mb-2 flex items-center justify-between">
                    <span className="text-[13px] font-semibold">{venueId}</span>
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
                    <label className="flex items-center gap-1.5">
                      <input
                        type="checkbox"
                        checked={v.fees.token_discount}
                        disabled={!editing || !mayEdit}
                        onChange={() => toggleDiscount(venueId)}
                      />
                      Token fee discount
                    </label>
                  </div>
                  <p className="mb-2 text-[11px] text-[var(--text-dim)]">
                    The discount rate, pay asset and API eligibility are a compiled-in per-venue constant
                    on the backend — this toggle only opts in. Some venues&apos; discounts exclude
                    API-executed trades; enabling those here is rejected with the reason shown above.
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
              ))}
            </div>
          );
        }}
      </Await>
      {previewState && current.kind === "ready" && (
        <PlatformApplyDialog
          state={previewState}
          fieldTiming={current.data.field_timing}
          onApplied={applied}
          onCancel={() => setPreviewState(null)}
        />
      )}
    </Section>
  );
}

// ---- Telegram allowlist (part of Notifications, BL-12) -------------------

export function TelegramAllowlistSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayEdit = can(role, "system:config");

  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.platform.current(), 15000, [refresh]);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<PlatformSettingsDoc | null>(null);
  const [idInput, setIdInput] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewErr, setPreviewErr] = useState("");
  const [previewState, setPreviewState] = useState<PreviewDraft | null>(null);

  const startEdit = (doc: PlatformSettingsDoc) => {
    setDraft(clonePlatformSettings(doc));
    setIdInput("");
    setMsg(null);
    setPreviewErr("");
    setEditing(true);
  };
  const discard = () => {
    setEditing(false);
    setDraft(null);
  };
  const addId = () => {
    const n = Number(idInput.trim());
    if (!Number.isInteger(n) || n <= 0) return;
    setDraft((d) => {
      if (!d) return d;
      const list = allowlistOf(d);
      return list.includes(n) ? d : { ...d, telegram: { allowlist: [...list, n].sort((a, b) => a - b) } };
    });
    setIdInput("");
  };
  const removeId = (n: number) => {
    setDraft((d) => (d ? { ...d, telegram: { allowlist: allowlistOf(d).filter((x) => x !== n) } } : d));
  };

  const review = () => {
    if (!draft) return;
    void runPreview(draft, setPreviewBusy, setPreviewErr, setPreviewState, setMsg);
  };
  const applied = (snap: PlatformSnapshotView) => {
    setMsg({ ok: true, text: `Version ${snap.version} active.` });
    setPreviewState(null);
    setEditing(false);
    setDraft(null);
    setRefresh((n) => n + 1);
  };

  return (
    <div className="mt-4 max-w-xl">
      <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
        Telegram allowlist
      </h3>
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      <Await state={current} what="platform settings">
        {(c) => {
          const doc = editing && draft ? draft : c.settings;
          const allowlist = allowlistOf(doc);
          const effect = effectForPath(c.field_timing, "telegram.allowlist");
          return (
            <>
              <div className="mb-2 flex items-center gap-2">
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
                {mayEdit && !editing && <Button onClick={() => startEdit(c.settings)}>Edit allowlist</Button>}
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

// ---- Platform settings version history (shared by both sections) -------

export function PlatformVersionHistorySection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayRollback = can(role, "exchange:config") || can(role, "system:config");

  const [refresh, setRefresh] = useState(0);
  const versions = usePoll(() => api.platform.versions(25), 15000, [refresh]);

  const [rollbackConfirm, setRollbackConfirm] = useState<{ version: number } | null>(null);
  const [rollbackLoading, setRollbackLoading] = useState<number | null>(null);
  const [rollbackErr, setRollbackErr] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const openRollback = async (version: number) => {
    setRollbackErr("");
    setRollbackLoading(version);
    try {
      await api.platform.version(version);
      setRollbackConfirm({ version });
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
      const snap = await api.platform.rollback(rollbackConfirm.version);
      setMsg({ ok: true, text: `Rolled back as new version ${snap.version}.` });
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Rollback failed." });
    } finally {
      setBusy(false);
      setRollbackConfirm(null);
    }
  };

  return (
    <Section title="Platform settings — version history">
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      {rollbackErr && <p className="mb-2 text-[12px] text-[var(--critical)]">{rollbackErr}</p>}
      <Await state={versions} what="platform settings versions">
        {(list) => (
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
                <Button key="rb" onClick={() => openRollback(v.version)} disabled={rollbackLoading === v.version}>
                  {rollbackLoading === v.version ? "Loading…" : "Roll back to"}
                </Button>
              ) : (
                ""
              ),
            ])}
          />
        )}
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
