"use client";

// Settings → Scanner Suite (design §7 GET/POST /screener/settings): poll
// interval, min liquidity, per-venue enabled/perps/fee fields, paper
// balances per venue. Same preview → ConfirmDialog(DiffTable) → apply
// shape as the platform sections, but the diff is computed client-side
// (presentation only, diffParams flattens+compares — §7 has no
// /screener/settings/preview route to call instead).

import { useState } from "react";
import {
  api,
  ApiError,
  isStaleVersion,
  staleVersion,
  type ScreenerSettingsDoc,
  type ScreenerVenueSettings,
} from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { diffParams, type DiffRow } from "@/lib/diff";
import {
  VENUE_OPTIONS,
  ScreenerAwait,
} from "@/components/screener/ScreenerShared";
import {
  Badge,
  Button,
  ConfirmDialog,
  DiffTable,
  Section,
  StaleVersionNotice,
  fmtTime,
} from "@/components/ui";

function cloneSettings(doc: ScreenerSettingsDoc): ScreenerSettingsDoc {
  return JSON.parse(JSON.stringify(doc)) as ScreenerSettingsDoc;
}

const DEFAULT_VENUE: ScreenerVenueSettings = {
  enabled: false,
  spot_taker_bps: "0",
  perp_taker_bps: "0",
  perps_enabled: false,
};

function venueOf(doc: ScreenerSettingsDoc, id: string): ScreenerVenueSettings {
  return doc.venues[id] ?? DEFAULT_VENUE;
}

function venueIds(doc: ScreenerSettingsDoc): string[] {
  return [...new Set([...VENUE_OPTIONS, ...Object.keys(doc.venues)])];
}

function balancesText(doc: ScreenerSettingsDoc, venue: string): string {
  const bal = doc.paper.balances[venue] ?? {};
  return Object.entries(bal)
    .map(([asset, amount]) => `${asset}=${amount}`)
    .join(", ");
}

function parseBalances(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const pair of text.split(",")) {
    const [asset, amount] = pair.split("=").map((s) => s.trim());
    if (asset && amount) out[asset] = amount;
  }
  return out;
}

function timingBadge(fieldTiming: Record<string, string>, path: string) {
  const t = fieldTiming[path] ?? "immediate";
  return <Badge tone={t === "immediate" ? "dim" : "warn"}>{t}</Badge>;
}

export function ScreenerSettingsSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayEdit = can(role, "screener:config");

  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.screener.settings.current(), 15000, [
    refresh,
  ]);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<ScreenerSettingsDoc | null>(null);
  const [parentVersion, setParentVersion] = useState<number | null>(null);
  const [balanceDrafts, setBalanceDrafts] = useState<Record<string, string>>(
    {},
  );
  const [confirmRows, setConfirmRows] = useState<DiffRow[] | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [stale, setStale] = useState<{ current: number | null } | null>(null);
  const [busy, setBusy] = useState(false);

  const startEdit = (snap: {
    version: number;
    settings: ScreenerSettingsDoc;
  }) => {
    const d = cloneSettings(snap.settings);
    setDraft(d);
    // Captured here, not re-read at review/apply time — usePoll can
    // advance the active version underneath an open editor (same
    // rationale as PlatformSections.tsx's PreviewDraft.parentVersion).
    setParentVersion(snap.version);
    const bd: Record<string, string> = {};
    for (const v of venueIds(d)) bd[v] = balancesText(d, v);
    setBalanceDrafts(bd);
    setMsg(null);
    setStale(null);
    setEditing(true);
  };

  const reloadAfterStale = () => {
    setStale(null);
    setEditing(false);
    setDraft(null);
    setConfirmRows(null);
    setRefresh((n) => n + 1);
  };

  const updateVenue = (id: string, patch: Partial<ScreenerVenueSettings>) => {
    setDraft((prev) => {
      if (!prev) return prev;
      const next = cloneSettings(prev);
      next.venues[id] = { ...venueOf(next, id), ...patch };
      return next;
    });
  };

  const review = (activeDoc: ScreenerSettingsDoc) => {
    if (!draft) return;
    const finalDraft = cloneSettings(draft);
    for (const [venue, text] of Object.entries(balanceDrafts)) {
      const parsed = parseBalances(text);
      if (Object.keys(parsed).length > 0)
        finalDraft.paper.balances[venue] = parsed;
      else delete finalDraft.paper.balances[venue];
    }
    setDraft(finalDraft);
    const rows = diffParams(
      activeDoc as unknown as Record<string, unknown>,
      finalDraft as unknown as Record<string, unknown>,
    );
    if (rows.length === 0) {
      setMsg({ ok: false, text: "No changes to apply." });
      return;
    }
    setConfirmRows(rows);
  };

  const confirmApply = async () => {
    if (!draft || parentVersion === null) return;
    setBusy(true);
    try {
      const snap = await api.screener.settings.apply(draft, parentVersion);
      setMsg({ ok: true, text: `Version ${snap.version} active.` });
      setEditing(false);
      setDraft(null);
      setConfirmRows(null);
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      if (isStaleVersion(err)) {
        setStale({ current: staleVersion(err) });
        setConfirmRows(null);
        return;
      }
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Apply failed.",
      });
      setConfirmRows(null);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section title="Scanner Suite">
      {stale && (
        <StaleVersionNotice
          currentVersion={stale.current}
          onReload={reloadAfterStale}
        />
      )}
      {msg && (
        <p
          className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
        >
          {msg.text}
        </p>
      )}
      <ScreenerAwait state={current} what="Scanner Suite settings">
        {(snap) => {
          const doc = editing && draft ? draft : snap.settings;
          return (
            <div className="max-w-4xl space-y-4 text-[13px]">
              <div className="flex items-center gap-2 text-[12px] text-[var(--text-dim)]">
                <Badge tone="dim">v{snap.version}</Badge>
                <span>applied {fmtTime(snap.created_at)}</span>
                {!editing && mayEdit && (
                  <Button onClick={() => startEdit(snap)}>Edit</Button>
                )}
              </div>

              <div className="flex flex-wrap gap-4">
                <label className="flex flex-col gap-1">
                  <span className="flex items-center gap-1 text-[12px] text-[var(--text-dim)]">
                    Poll interval (s){" "}
                    {timingBadge(snap.field_timing, "poll_interval_s")}
                  </span>
                  <input
                    value={doc.poll_interval_s}
                    disabled={!editing}
                    onChange={(e) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              poll_interval_s: Number(e.target.value) || 0,
                            }
                          : prev,
                      )
                    }
                    inputMode="numeric"
                    className="w-28 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none disabled:opacity-60"
                  />
                </label>
                <label className="flex flex-col gap-1">
                  <span className="flex items-center gap-1 text-[12px] text-[var(--text-dim)]">
                    Min liquidity (quote){" "}
                    {timingBadge(snap.field_timing, "min_liquidity_quote")}
                  </span>
                  <input
                    value={doc.min_liquidity_quote}
                    disabled={!editing}
                    onChange={(e) =>
                      setDraft((prev) =>
                        prev
                          ? { ...prev, min_liquidity_quote: e.target.value }
                          : prev,
                      )
                    }
                    inputMode="decimal"
                    className="w-40 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none disabled:opacity-60"
                  />
                </label>
              </div>

              <div className="overflow-x-auto rounded border border-[var(--border)]">
                <table className="w-full border-collapse text-[13px]">
                  <thead>
                    <tr className="bg-[var(--bg-panel)] text-left">
                      <th className="px-3 py-2 font-medium text-[var(--text-dim)]">
                        Venue
                      </th>
                      <th className="px-3 py-2 font-medium text-[var(--text-dim)]">
                        Enabled
                      </th>
                      <th className="px-3 py-2 font-medium text-[var(--text-dim)]">
                        Perps enabled
                      </th>
                      <th className="px-3 py-2 font-medium text-[var(--text-dim)]">
                        Spot taker (bps)
                      </th>
                      <th className="px-3 py-2 font-medium text-[var(--text-dim)]">
                        Perp taker (bps)
                      </th>
                      <th className="px-3 py-2 font-medium text-[var(--text-dim)]">
                        Paper balances
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {venueIds(doc).map((id) => {
                      const v = venueOf(doc, id);
                      return (
                        <tr
                          key={id}
                          className="border-t border-[var(--border)]"
                        >
                          <td className="px-3 py-1.5">{id}</td>
                          <td className="px-3 py-1.5">
                            <input
                              type="checkbox"
                              checked={v.enabled}
                              disabled={!editing}
                              onChange={(e) =>
                                updateVenue(id, { enabled: e.target.checked })
                              }
                            />
                          </td>
                          <td className="px-3 py-1.5">
                            <input
                              type="checkbox"
                              checked={v.perps_enabled}
                              disabled={!editing}
                              onChange={(e) =>
                                updateVenue(id, {
                                  perps_enabled: e.target.checked,
                                })
                              }
                            />
                          </td>
                          <td className="px-3 py-1.5">
                            <input
                              value={v.spot_taker_bps}
                              disabled={!editing}
                              onChange={(e) =>
                                updateVenue(id, {
                                  spot_taker_bps: e.target.value,
                                })
                              }
                              inputMode="decimal"
                              className="w-20 rounded border border-[var(--border)] bg-[var(--bg)] px-1.5 py-0.5 outline-none disabled:opacity-60"
                            />
                          </td>
                          <td className="px-3 py-1.5">
                            <input
                              value={v.perp_taker_bps}
                              disabled={!editing}
                              onChange={(e) =>
                                updateVenue(id, {
                                  perp_taker_bps: e.target.value,
                                })
                              }
                              inputMode="decimal"
                              className="w-20 rounded border border-[var(--border)] bg-[var(--bg)] px-1.5 py-0.5 outline-none disabled:opacity-60"
                            />
                          </td>
                          <td className="px-3 py-1.5">
                            <input
                              value={
                                editing
                                  ? (balanceDrafts[id] ?? "")
                                  : balancesText(doc, id)
                              }
                              disabled={!editing}
                              placeholder="USDT=10000, BTC=0.5"
                              onChange={(e) =>
                                setBalanceDrafts((prev) => ({
                                  ...prev,
                                  [id]: e.target.value,
                                }))
                              }
                              className="w-56 rounded border border-[var(--border)] bg-[var(--bg)] px-1.5 py-0.5 outline-none disabled:opacity-60"
                            />
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              {editing && (
                <div className="flex gap-2">
                  <Button onClick={() => review(snap.settings)}>
                    Review changes
                  </Button>
                  <Button
                    onClick={() => {
                      setEditing(false);
                      setDraft(null);
                      setConfirmRows(null);
                    }}
                    danger
                  >
                    Cancel
                  </Button>
                </div>
              )}

              {confirmRows && (
                <ConfirmDialog
                  title="Apply new Scanner Suite settings?"
                  confirmLabel={busy ? "Applying…" : "Apply"}
                  confirmDisabled={busy}
                  onConfirm={confirmApply}
                  onCancel={() => setConfirmRows(null)}
                  body={
                    <>
                      <p className="mb-3">
                        This becomes a new Scanner Suite settings version on top
                        of v{snap.version}.
                      </p>
                      <DiffTable
                        rows={confirmRows}
                        beforeLabel={`Current (v${snap.version})`}
                        afterLabel="New (draft)"
                      />
                    </>
                  }
                />
              )}
            </div>
          );
        }}
      </ScreenerAwait>
    </Section>
  );
}
