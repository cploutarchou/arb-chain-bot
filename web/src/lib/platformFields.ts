// Shared client-side helpers for the Settings → Markets & assets / Venues &
// fees / Notifications (Telegram allowlist) editors (T-057). Every write
// still goes through preview → ConfirmDialog → apply against the real
// backend; this module only builds the DRAFT document and keeps it
// internally consistent enough to pass platform.Settings.Validate's
// structural checks (exact-set-equality on paper.balances, overrides ⊆
// symbols) before the backend gets to validate the substance.

import type {
  PlatformFeeOverride,
  PlatformSettingsDoc,
  PlatformVenueSettings,
} from "@/lib/api/client";

// clonePlatformSettings deep-clones a settings document so drafts never
// mutate the polled/active snapshot in place (mirrors strategy Params
// clone hygiene already used on /strategies). Also normalizes
// telegram.allowlist: Go's []int64(nil) — the boot default before any
// allowlist entry is ever added — marshals as JSON `null`, not `[]`.
export function clonePlatformSettings(doc: PlatformSettingsDoc): PlatformSettingsDoc {
  const cloned = JSON.parse(JSON.stringify(doc)) as PlatformSettingsDoc;
  cloned.telegram.allowlist = cloned.telegram.allowlist ?? [];
  return cloned;
}

// allowlistOf reads telegram.allowlist defensively — see
// clonePlatformSettings's comment; this covers the read-only path, which
// renders the polled snapshot directly rather than a cloned draft.
export function allowlistOf(doc: PlatformSettingsDoc): number[] {
  return doc.telegram.allowlist ?? [];
}

// effectForPath resolves a dotted diff/field path against the
// backend-supplied field_timing map (never hardcoded, per design §1.3/§3).
// field_timing keys are field-level ("venues.binance.fees.overrides.BTCUSDT")
// while diff paths can be leaf-level for maps ("...overrides.BTCUSDT.maker_bps"),
// so this does an exact match first, then the longest key that is a strict
// prefix of path, and only falls back to "restart" (every field's default
// per the design) when field_timing has nothing for it at all.
export function effectForPath(fieldTiming: Record<string, string>, path: string): "hot" | "restart" {
  const exact = fieldTiming[path];
  if (exact === "hot" || exact === "restart") return exact;
  let best: string | null = null;
  for (const key of Object.keys(fieldTiming)) {
    if (path === key || path.startsWith(`${key}.`)) {
      if (best === null || key.length > best.length) best = key;
    }
  }
  if (best) {
    const v = fieldTiming[best];
    if (v === "hot" || v === "restart") return v;
  }
  return "restart";
}

export function timingLabel(effect: "hot" | "restart"): string {
  return effect === "hot" ? "Immediate" : "On restart";
}

// enabledStartingAssets is the union of starting_assets across every
// ENABLED venue in doc — the exact set platform.PaperSettings.validate
// requires paper.balances' keys to equal (settings.go:222-247).
export function enabledStartingAssets(doc: PlatformSettingsDoc): string[] {
  const wanted = new Set<string>();
  for (const v of Object.values(doc.venues)) {
    if (!v.enabled) continue;
    for (const a of v.starting_assets) wanted.add(a);
  }
  return [...wanted].sort();
}

// syncPaperBalances recomputes paper.balances' key set to match
// enabledStartingAssets, keeping any existing value for an asset that is
// still wanted and seeding new assets with defaultBalance (an editable UI
// default, not a computed financial figure — the operator reviews and can
// change it before Apply). Call this after any edit that can change the
// enabled/starting-asset union: venue enabled toggle or starting_assets
// add/remove.
export function syncPaperBalances(doc: PlatformSettingsDoc, defaultBalance = "10000"): PlatformSettingsDoc {
  const wanted = enabledStartingAssets(doc);
  const balances: Record<string, string> = {};
  for (const a of wanted) {
    balances[a] = doc.paper.balances[a] ?? defaultBalance;
  }
  return { ...doc, paper: { balances } };
}

// pruneOverrides drops any fee override whose symbol is no longer in the
// venue's symbol list — platform.FeeSettings.validate rejects an override
// key that isn't in symbols (settings.go:199-202).
export function pruneOverrides(venue: PlatformVenueSettings): PlatformVenueSettings {
  if (!venue.fees.overrides || Object.keys(venue.fees.overrides).length === 0) return venue;
  const symbolSet = new Set(venue.symbols);
  const overrides: Record<string, PlatformFeeOverride> = {};
  for (const [sym, o] of Object.entries(venue.fees.overrides)) {
    if (symbolSet.has(sym)) overrides[sym] = o;
  }
  return {
    ...venue,
    fees: { ...venue.fees, overrides: Object.keys(overrides).length ? overrides : undefined },
  };
}

// updateVenue returns a new document with venues[venueId] replaced by
// fn(current venue) — the one mutation point every editor goes through so
// paper.balances/overrides stay in sync afterward via the caller's own
// sync* call.
export function updateVenue(
  doc: PlatformSettingsDoc,
  venueId: string,
  fn: (v: PlatformVenueSettings) => PlatformVenueSettings,
): PlatformSettingsDoc {
  const venue = doc.venues[venueId];
  if (!venue) return doc;
  return { ...doc, venues: { ...doc.venues, [venueId]: fn(venue) } };
}

// normalizeSymbol/normalizeAsset apply the same upper-case, trim rule the
// backend enforces (settings.go: "must be upper-case") so an add doesn't
// round-trip through a preview rejection for casing alone.
export function normalizeToken(raw: string): string {
  return raw.trim().toUpperCase();
}
