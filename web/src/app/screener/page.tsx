"use client";

// Cross-venue spot screener (design §5/§7 GET /screener/spreads): a
// compact status strip, a common/Advanced filter card with applied-
// filter chips, a dense auto-refreshing table at bounded decimal
// precision, and a row-expand detail drawer (never an inline table row —
// that would break VirtualTable's fixed-row-height windowing above 500
// rows; design-system.md §4.3 / UX §4).
//
// T-087 redesign (docs/design/client-area-audit-2026-09-12 §2/§3), what
// changed and why:
//   §A1 seven large Stat cards -> one MetricStrip row, same numbers.
//   §A2/§A3 fully-expanded filter form -> common fields (base/pair, buy
//     venues, sell venues, quote, min net spread) always visible; base
//     deny-list, min liquidity, min lifetime, the two safe-default
//     opt-ins and the saved-template row behind an Advanced disclosure
//     (collapsed by default, remembered per browser); applied filters
//     render as removable chips plus one Clear-filters action.
//   §A4 ten columns with 20+ digit bps cells -> seven decision-relevant
//     columns at bounded precision (pair, buy venue/ask, sell venue/bid,
//     net spread, liquidity, freshness/limitations, detail); gross bps
//     and lifetime move behind an optional-column toggle; the two raw
//     network chips fold into the freshness/limitations cell.
//   §A5 caveats (liquidity_unknown, suspect + suspect_reason, stale book
//     age via lib/format's bookAgeText/bookAgeStaleMs, unknown network
//     status) render inline next to the value they qualify, not as a
//     footnote list under the table.
//   §A6 the selected row survives a background refresh: while its key is
//     still present in the polled rows the drawer shows the freshest
//     copy; once it drops out, the drawer keeps the last known values
//     and says so, rather than closing or silently swapping in another
//     row (RowDrawer's own focus-return-on-unmount effect must only fire
//     when the operator actually closes it).
//   §C6 the Calculator hand-off is two-way: this page seeds its base/
//     quote/buy-venue/sell-venue from the query string a "Back to
//     Screener" link can carry, in addition to the existing Detail ->
//     Calculator hand-off.

import { Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import {
  api,
  ApiError,
  type ScreenerFilterSet,
  type ScreenerSpreadRow,
} from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { bookAgeStaleMs, bookAgeText } from "@/lib/format";
import {
  presentBps,
  presentFeeBps,
  presentPrice,
  presentQty,
  presentQuote,
} from "@/lib/decimal";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  NO_TRANSFER_NOTE,
  NetworkBadge,
  ScreenerAwait,
  MetricStrip,
  VenueChips,
  fmtAge,
  parseCsv,
  pollIntervalSFromStatus,
  pollMsFromStatus,
  staleCellClass,
  useScreenerStatus,
  type MetricStripItem,
} from "@/components/screener/ScreenerShared";
import {
  Badge,
  Button,
  DecimalValue,
  Loading,
  PageTitle,
  Section,
  VirtualTable,
  fmtTime,
  type ColumnAlign,
} from "@/components/ui";
import {
  FilterCard,
  FilterRow,
  NumericFilterField,
  SelectFilterField,
  TextFilterField,
  type FilterChip,
} from "@/components/FilterCard";
import { RowDrawer } from "@/components/RowDrawer";
import { ExternalIcon } from "@/components/icons";

const QUOTE_OPTIONS = ["", "USDT", "USDC", "BTC", "ETH"];

function rowKey(r: ScreenerSpreadRow): string {
  return `${r.base}/${r.quote}:${r.buy_venue}->${r.sell_venue}`;
}

function toggleIn(list: string[], v: string): string[] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
}

// parseVenueParam reads a venue filter from the URL. Comma-separated so a
// multi-venue selection round-trips, while the single value the
// Calculator hand-off writes keeps working unchanged.
function parseVenueParam(raw: string | null): string[] {
  if (!raw) return [];
  return raw
    .split(",")
    .map((v) => v.trim().toLowerCase())
    .filter((v) => v !== "");
}

// isTruthyParam: only an explicit affirmative enables an opt-in. The two
// opt-ins re-admit lanes the backend excludes by default (suspect
// asset-identity and unknown liquidity), so a malformed or unexpected
// value must fall back to the safe default rather than to "on".
function isTruthyParam(raw: string | null): boolean {
  return raw === "1" || raw === "true";
}

// FILTER_PARAMS is the single list of query keys this page owns. The sync
// effect deletes exactly these before rewriting them, so any other
// parameter on the URL — a hand-off marker, something a future feature
// adds — is preserved rather than silently dropped.
const FILTER_PARAMS = [
  "base",
  "quote",
  "buy_venue",
  "sell_venue",
  "min_spread_bps",
  "min_liquidity",
  "min_lifetime_s",
  "bases_deny",
  "include_suspect",
  "include_unknown_liquidity",
] as const;

function ScreenerPageInner() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayConfig = can(role, "screener:config");
  const router = useRouter();
  const params = useSearchParams();
  const pathname = usePathname();

  const status = useScreenerStatus();
  const pollMs = pollMsFromStatus(status);
  const pollIntervalS = pollIntervalSFromStatus(status);

  // Seeded once from a Calculator "Back to Screener" hand-off (§C6) —
  // read at mount only; the operator's own edits afterwards are the
  // source of truth.
  // Every filter is seeded from the URL, not only the four the Calculator
  // hand-off used to carry. Before this, six of the ten travelled in
  // neither direction: a screener view could not be bookmarked, shared or
  // restored by Back, which is what console-v2.md §6.1 asked for and
  // never got.
  //
  // The text filters are seeded as the **strings** they were typed as and
  // are written back verbatim. They are never parsed to a number on the
  // way through the URL: `min_liquidity` is money, and a round trip
  // through a float is exactly the shortcut decimal.ts exists to remove.
  const [buyVenues, setBuyVenues] = useState<string[]>(() =>
    parseVenueParam(params.get("buy_venue")),
  );
  const [sellVenues, setSellVenues] = useState<string[]>(() =>
    parseVenueParam(params.get("sell_venue")),
  );
  const [quote, setQuote] = useState(
    () => params.get("quote")?.toUpperCase() ?? "",
  );
  const [minSpreadBps, setMinSpreadBps] = useState(
    () => params.get("min_spread_bps") ?? "",
  );
  const [minLiquidity, setMinLiquidity] = useState(
    () => params.get("min_liquidity") ?? "",
  );
  const [minLifetimeS, setMinLifetimeS] = useState(
    () => params.get("min_lifetime_s") ?? "",
  );
  const [basesAllowText, setBasesAllowText] = useState(
    () => params.get("base")?.toUpperCase() ?? "",
  );
  const [basesDenyText, setBasesDenyText] = useState(
    () => params.get("bases_deny")?.toUpperCase() ?? "",
  );
  // Both OFF by default, matching the backend's own safe default
  // (handleScreenerSpreads excludes suspect/unknown-liquidity lanes
  // unless explicitly opted in) — never change this default.
  //
  // They are read from the URL, but only an explicit affirmative turns
  // them on: anything else, including a malformed value, leaves the safe
  // default in place. A link must not be able to quietly re-admit
  // suspect lanes through a typo.
  const [includeSuspect, setIncludeSuspect] = useState(() =>
    isTruthyParam(params.get("include_suspect")),
  );
  const [includeUnknownLiquidity, setIncludeUnknownLiquidity] = useState(() =>
    isTruthyParam(params.get("include_unknown_liquidity")),
  );
  // Optional columns (§A4) — off by default, the table stays at the
  // seven decision-relevant columns until an operator asks for more.
  const [showGrossColumn, setShowGrossColumn] = useState(false);
  const [showLifetimeColumn, setShowLifetimeColumn] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [lastKnownRow, setLastKnownRow] = useState<ScreenerSpreadRow | null>(
    null,
  );
  const expandTriggerRef = useRef<HTMLElement | null>(null);

  const basesAllow = useMemo(() => parseCsv(basesAllowText), [basesAllowText]);
  const basesDeny = useMemo(() => parseCsv(basesDenyText), [basesDenyText]);

  const activeFilterCount = [
    buyVenues.length > 0,
    sellVenues.length > 0,
    quote !== "",
    minSpreadBps.trim() !== "",
    minLiquidity.trim() !== "",
    minLifetimeS.trim() !== "",
    basesAllowText.trim() !== "",
    basesDenyText.trim() !== "",
    includeSuspect,
    includeUnknownLiquidity,
  ].filter(Boolean).length;

  // ---- URL sync (console-v2.md §6.1) -------------------------------------
  // The filter state is written back to the query string, so a screener
  // view can be bookmarked, shared, and restored by Back — and so the
  // Calculator hand-off returns to the filters it left rather than to a
  // default table.
  //
  // Three things this deliberately does:
  //   * `replace`, never `push`: typing in a filter must not manufacture
  //     a history entry per keystroke, or Back becomes unusable.
  //   * unknown parameters are preserved. Only the keys this page owns
  //     (FILTER_PARAMS) are cleared before rewriting.
  //   * the values are the **strings as typed**. `min_liquidity` is
  //     money; round-tripping it through a number here would reintroduce
  //     the float shortcut the rest of this change removes.
  const filterQuery = useMemo(() => {
    const next = new URLSearchParams(params.toString());
    for (const key of FILTER_PARAMS) next.delete(key);
    if (basesAllowText.trim())
      next.set("base", basesAllowText.trim().toUpperCase());
    if (quote.trim()) next.set("quote", quote.trim().toUpperCase());
    if (buyVenues.length > 0) next.set("buy_venue", buyVenues.join(","));
    if (sellVenues.length > 0) next.set("sell_venue", sellVenues.join(","));
    if (minSpreadBps.trim()) next.set("min_spread_bps", minSpreadBps.trim());
    if (minLiquidity.trim()) next.set("min_liquidity", minLiquidity.trim());
    if (minLifetimeS.trim()) next.set("min_lifetime_s", minLifetimeS.trim());
    if (basesDenyText.trim())
      next.set("bases_deny", basesDenyText.trim().toUpperCase());
    // Written only when ON. An absent parameter therefore means the safe
    // default, and a shared link never carries an opt-in the sender did
    // not actually enable.
    if (includeSuspect) next.set("include_suspect", "1");
    if (includeUnknownLiquidity) next.set("include_unknown_liquidity", "1");
    next.sort();
    return next.toString();
  }, [
    params,
    basesAllowText,
    quote,
    buyVenues,
    sellVenues,
    minSpreadBps,
    minLiquidity,
    minLifetimeS,
    basesDenyText,
    includeSuspect,
    includeUnknownLiquidity,
  ]);

  // lastWritten records the query this component last put in the address
  // bar, which is what lets it tell its own writes apart from someone
  // else's.
  const lastWritten = useRef<string | null>(null);

  useEffect(() => {
    const current = new URLSearchParams(params.toString());
    current.sort();
    const currentStr = current.toString();
    if (currentStr === filterQuery) return;

    // The URL changed from outside this component — Back or Forward, or a
    // "Back to Screener" link arriving with filters on it. Adopt it.
    //
    // Without this the sync effect would treat the difference as "my
    // state is newer" and rewrite the address bar from filters that were
    // seeded at mount, which would make Back appear to do nothing and
    // would silently discard the filters a hand-off link carried. Next
    // may reuse this component across such a navigation rather than
    // remounting it, so seeding at mount is not enough on its own.
    if (lastWritten.current !== null && currentStr !== lastWritten.current) {
      lastWritten.current = currentStr;
      setBuyVenues(parseVenueParam(params.get("buy_venue")));
      setSellVenues(parseVenueParam(params.get("sell_venue")));
      setQuote(params.get("quote")?.toUpperCase() ?? "");
      setMinSpreadBps(params.get("min_spread_bps") ?? "");
      setMinLiquidity(params.get("min_liquidity") ?? "");
      setMinLifetimeS(params.get("min_lifetime_s") ?? "");
      setBasesAllowText(params.get("base")?.toUpperCase() ?? "");
      setBasesDenyText(params.get("bases_deny")?.toUpperCase() ?? "");
      setIncludeSuspect(isTruthyParam(params.get("include_suspect")));
      setIncludeUnknownLiquidity(
        isTruthyParam(params.get("include_unknown_liquidity")),
      );
      return;
    }

    // Debounced: a text filter would otherwise issue a soft navigation on
    // every keystroke. The delay is short enough that Back and bookmark
    // both see the settled state.
    const t = setTimeout(() => {
      lastWritten.current = filterQuery;
      router.replace(filterQuery ? `${pathname}?${filterQuery}` : pathname, {
        scroll: false,
      });
    }, 300);
    return () => clearTimeout(t);
  }, [filterQuery, params, pathname, router]);

  const spreads = usePoll(
    () =>
      api.screener.spreads({
        min_spread_bps: minSpreadBps.trim() ? Number(minSpreadBps) : undefined,
        min_liquidity: minLiquidity.trim() ? Number(minLiquidity) : undefined,
        min_lifetime_s: minLifetimeS.trim() ? Number(minLifetimeS) : undefined,
        buy: buyVenues.length ? buyVenues : undefined,
        sell: sellVenues.length ? sellVenues : undefined,
        quote: quote || undefined,
        base: basesAllow.length ? basesAllow.join(",") : undefined,
        include_suspect: includeSuspect || undefined,
        include_unknown_liquidity: includeUnknownLiquidity || undefined,
        limit: 200,
      }),
    pollMs,
    [
      pollMs,
      buyVenues.join(","),
      sellVenues.join(","),
      quote,
      minSpreadBps,
      minLiquidity,
      minLifetimeS,
      basesAllowText,
      includeSuspect,
      includeUnknownLiquidity,
    ],
  );

  // Templates (per user, §7 GET/POST/DELETE /screener/templates).
  const [templateRefresh, setTemplateRefresh] = useState(0);
  const templates = usePoll(() => api.screener.templates.list(), 15000, [
    templateRefresh,
  ]);
  const [templateName, setTemplateName] = useState("");
  const [templateMsg, setTemplateMsg] = useState<{
    ok: boolean;
    text: string;
  } | null>(null);

  const currentFilterSet = (): ScreenerFilterSet => ({
    buy_venues: buyVenues.length ? buyVenues : undefined,
    sell_venues: sellVenues.length ? sellVenues : undefined,
    quote: quote || undefined,
    min_spread_bps: minSpreadBps.trim() ? Number(minSpreadBps) : undefined,
    min_liquidity: minLiquidity.trim() ? Number(minLiquidity) : undefined,
    min_lifetime_s: minLifetimeS.trim() ? Number(minLifetimeS) : undefined,
    bases_allow: basesAllow.length ? basesAllow : undefined,
    bases_deny: basesDeny.length ? basesDeny : undefined,
  });

  const loadTemplate = (f: ScreenerFilterSet) => {
    setBuyVenues(f.buy_venues ?? []);
    setSellVenues(f.sell_venues ?? []);
    setQuote(f.quote ?? "");
    setMinSpreadBps(
      f.min_spread_bps !== undefined ? String(f.min_spread_bps) : "",
    );
    setMinLiquidity(
      f.min_liquidity !== undefined ? String(f.min_liquidity) : "",
    );
    setMinLifetimeS(
      f.min_lifetime_s !== undefined ? String(f.min_lifetime_s) : "",
    );
    setBasesAllowText((f.bases_allow ?? []).join(", "));
    setBasesDenyText((f.bases_deny ?? []).join(", "));
  };

  // Clear filters (§A3) — resets every field this page owns, including
  // the two opt-ins back to their safe-default OFF state (never leaves
  // them on after a clear).
  const clearAllFilters = () => {
    setBuyVenues([]);
    setSellVenues([]);
    setQuote("");
    setMinSpreadBps("");
    setMinLiquidity("");
    setMinLifetimeS("");
    setBasesAllowText("");
    setBasesDenyText("");
    setIncludeSuspect(false);
    setIncludeUnknownLiquidity(false);
  };

  const saveTemplate = async () => {
    setTemplateMsg(null);
    if (!templateName.trim()) {
      setTemplateMsg({ ok: false, text: "Name the template first." });
      return;
    }
    try {
      await api.screener.templates.create(
        templateName.trim(),
        currentFilterSet(),
      );
      setTemplateMsg({ ok: true, text: `Saved "${templateName.trim()}".` });
      setTemplateName("");
      setTemplateRefresh((n) => n + 1);
    } catch (err: unknown) {
      setTemplateMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Save failed.",
      });
    }
  };

  const deleteTemplate = async (id: string) => {
    setTemplateMsg(null);
    try {
      await api.screener.templates.remove(id);
      setTemplateRefresh((n) => n + 1);
    } catch (err: unknown) {
      setTemplateMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Delete failed.",
      });
    }
  };

  const allRows = spreads.kind === "ready" ? (spreads.data.rows ?? []) : [];
  // bases_deny has no wire slot (§7) — applied to the fetched page here.
  const rows = basesDeny.length
    ? allRows.filter((r) => !basesDeny.includes(r.base.toUpperCase()))
    : allRows;

  // The selected row survives a background refresh (§A6): while its key
  // is still present in the freshly polled rows, keep the freshest copy;
  // once it drops out (filtered out, expired, evicted), freeze the last
  // known copy instead of unmounting the drawer or silently showing a
  // different row. RowDrawer's own unmount effect returns focus to
  // `expandTriggerRef` — that must only fire when the operator actually
  // closes the drawer, never mid-poll, which is exactly what keeping
  // `expanded` (the key) truthy across a poll guarantees.
  const liveExpandedRow = expanded
    ? rows.find((r) => rowKey(r) === expanded)
    : undefined;
  useEffect(() => {
    if (liveExpandedRow) setLastKnownRow(liveExpandedRow);
  }, [liveExpandedRow]);
  const expandedRow =
    liveExpandedRow ??
    (expanded && lastKnownRow && rowKey(lastKnownRow) === expanded
      ? lastKnownRow
      : undefined);
  const expandedRowPresent = !!liveExpandedRow;

  const openCalculator = (r: ScreenerSpreadRow) => {
    const p = new URLSearchParams({
      base: r.base,
      quote: r.quote,
      buy_venue: r.buy_venue,
      sell_venue: r.sell_venue,
    });
    router.push(`/calculator?${p.toString()}`);
  };

  // ---- Status strip (§A1): every number the seven former Stat cards
  // carried, in one row. Each item degrades to "…"/"—" on its own
  // (loading vs never-loaded) rather than blocking the whole strip on
  // two independent polls (status, spreads).
  const statusItems: MetricStripItem[] = [];
  if (status.kind === "ready") {
    const s = status.data;
    const online = (s.venues ?? []).filter((v) => v.online).length;
    const total = (s.venues ?? []).length;
    const ageMs = Math.max(0, Date.now() - new Date(s.updated_at).getTime());
    statusItems.push(
      {
        label: "Venues online",
        value: `${online} / ${total}`,
        tone: online === total && total > 0 ? "ok" : "warn",
      },
      { label: "Pairs tracked", value: s.pairs_tracked },
      { label: "Spreads / sec", value: s.spreads_per_sec },
      { label: "Data age", value: fmtAge(ageMs) },
    );
  } else if (status.kind === "loading") {
    statusItems.push(
      { label: "Venues online", value: "…" },
      { label: "Pairs tracked", value: "…" },
      { label: "Spreads / sec", value: "…" },
      { label: "Data age", value: "…" },
    );
  } else {
    // A failed status call is not a reading of zero. An em dash here was
    // indistinguishable from a healthy-but-empty deployment — "no venues
    // configured, nothing tracked yet" — while the spreads table below
    // carried on rendering, so nothing on the page said the venue-health
    // source was unreachable. The word and the tone both say it now, and
    // the backend's own message rides along as the hint.
    statusItems.push(
      { label: "Venues online", value: "unavailable", tone: "bad", hint: status.message },
      { label: "Pairs tracked", value: "unavailable", tone: "bad", hint: status.message },
      { label: "Spreads / sec", value: "unavailable", tone: "bad", hint: status.message },
      { label: "Data age", value: "unavailable", tone: "bad", hint: status.message },
    );
  }
  // The poll interval is a backend-owned figure that falls back to a
  // local default when status is unreadable — so it must not be stated
  // as fact while the endpoint that owns it is down.
  statusItems.push(
    status.kind === "error"
      ? {
          label: "Poll interval",
          value: `${pollIntervalS}s (default)`,
          tone: "dim",
          hint: "The configured interval could not be read; this is the console's fallback.",
        }
      : { label: "Poll interval", value: `${pollIntervalS}s` },
  );
  if (spreads.kind === "ready" && spreads.data.excluded) {
    const { suspect, liquidity_unknown } = spreads.data.excluded;
    statusItems.push(
      {
        label: "Excluded: suspect",
        value: suspect,
        tone: suspect > 0 ? "warn" : "dim",
        hint: 'Lanes hidden by the asset-identity guard. Open "Advanced filters" and check "Include suspect lanes" to show them.',
      },
      {
        label: "Excluded: unknown liquidity",
        value: liquidity_unknown,
        tone: liquidity_unknown > 0 ? "warn" : "dim",
        hint: 'Lanes hidden because top-of-book size is unknown. Open "Advanced filters" and check "Include unknown-liquidity lanes" to show them.',
      },
    );
  } else {
    const placeholder = spreads.kind === "loading" ? "…" : "—";
    statusItems.push(
      { label: "Excluded: suspect", value: placeholder },
      { label: "Excluded: unknown liquidity", value: placeholder },
    );
  }

  // ---- Applied-filter chips (§A3) ------------------------------------
  const chips: FilterChip[] = [];
  if (basesAllowText.trim())
    chips.push({
      key: "base",
      label: `Base: ${basesAllowText.trim()}`,
      onRemove: () => setBasesAllowText(""),
    });
  if (buyVenues.length)
    chips.push({
      key: "buy",
      label: `Buy: ${buyVenues.join(", ")}`,
      onRemove: () => setBuyVenues([]),
    });
  if (sellVenues.length)
    chips.push({
      key: "sell",
      label: `Sell: ${sellVenues.join(", ")}`,
      onRemove: () => setSellVenues([]),
    });
  if (quote)
    chips.push({
      key: "quote",
      label: `Quote: ${quote}`,
      onRemove: () => setQuote(""),
    });
  if (minSpreadBps.trim())
    chips.push({
      key: "minspread",
      label: `Min net spread: ${minSpreadBps.trim()} bps`,
      onRemove: () => setMinSpreadBps(""),
    });
  if (minLiquidity.trim())
    chips.push({
      key: "minliq",
      label: `Min liquidity: ${minLiquidity.trim()}`,
      onRemove: () => setMinLiquidity(""),
    });
  if (minLifetimeS.trim())
    chips.push({
      key: "minlife",
      label: `Min lifetime: ${minLifetimeS.trim()}s`,
      onRemove: () => setMinLifetimeS(""),
    });
  if (basesDenyText.trim())
    chips.push({
      key: "deny",
      label: `Excluding: ${basesDenyText.trim()}`,
      onRemove: () => setBasesDenyText(""),
    });
  if (includeSuspect)
    chips.push({
      key: "suspect",
      label: "Include suspect lanes",
      onRemove: () => setIncludeSuspect(false),
    });
  if (includeUnknownLiquidity)
    chips.push({
      key: "unknownliq",
      label: "Include unknown-liquidity lanes",
      onRemove: () => setIncludeUnknownLiquidity(false),
    });

  // ---- Table columns (§A4): seven decision-relevant columns by
  // default; gross bps and lifetime are opt-in extra columns.
  const head: string[] = [
    "Pair",
    "Buy venue / ask",
    "Sell venue / bid",
    "Net spread",
    "Liquidity",
  ];
  const align: ColumnAlign[] = ["text", "text", "text", "num", "num"];
  if (showGrossColumn) {
    head.push("Gross bps");
    align.push("num");
  }
  if (showLifetimeColumn) {
    head.push("Lifetime (s)");
    align.push("num");
  }
  head.push("Freshness / limitations", "");
  align.push("text", "text");

  return (
    <ConsoleShell>
      <PageTitle>Screener</PageTitle>

      <MetricStrip items={statusItems} />

      <FilterCard
        activeCount={activeFilterCount}
        chips={chips}
        onClearAll={chips.length > 0 ? clearAllFilters : undefined}
        advancedStorageKey="arb-console.screener-filters.advanced-open"
        advanced={
          <>
            <div className="flex flex-wrap items-end gap-3">
              <TextFilterField
                label="Base deny-list (comma-separated)"
                value={basesDenyText}
                onChange={setBasesDenyText}
                placeholder="SHIB, PEPE"
                width="flex-1 min-w-[220px]"
                hint="Applied to the fetched rows in this browser only — /screener/spreads has no deny parameter."
              />
              <NumericFilterField
                label="Min liquidity"
                value={minLiquidity}
                onChange={setMinLiquidity}
                unit="quote"
                width="w-32"
              />
              <NumericFilterField
                label="Min lifetime"
                value={minLifetimeS}
                onChange={setMinLifetimeS}
                unit="s"
                width="w-24"
                inputMode="numeric"
              />
            </div>
            <div className="flex flex-wrap items-center gap-4">
              <label className="flex items-center gap-2 text-[12px]">
                <input
                  type="checkbox"
                  checked={includeSuspect}
                  onChange={(e) => setIncludeSuspect(e.target.checked)}
                />
                Include suspect lanes (asset-identity guard)
              </label>
              <label className="flex items-center gap-2 text-[12px]">
                <input
                  type="checkbox"
                  checked={includeUnknownLiquidity}
                  onChange={(e) =>
                    setIncludeUnknownLiquidity(e.target.checked)
                  }
                />
                Include unknown-liquidity lanes
              </label>
            </div>
            <div className="flex flex-wrap items-center gap-4 border-t border-[var(--border)] pt-3">
              <label className="flex items-center gap-2 text-[12px]">
                <input
                  type="checkbox"
                  checked={showGrossColumn}
                  onChange={(e) => setShowGrossColumn(e.target.checked)}
                />
                Show gross bps column
              </label>
              <label className="flex items-center gap-2 text-[12px]">
                <input
                  type="checkbox"
                  checked={showLifetimeColumn}
                  onChange={(e) => setShowLifetimeColumn(e.target.checked)}
                />
                Show lifetime column
              </label>
            </div>

            {/* Templates are a per-user read (§7: "GET /screener/templates
                ... (per user)") — VIEWER/OPERATOR can load their own saved
                filters same as ADMIN; only saving a new one and deleting
                are mutations gated behind screener:config. */}
            <div className="flex flex-wrap items-end gap-3 border-t border-[var(--border)] pt-3">
              {mayConfig && (
                <>
                  <TextFilterField
                    label="Save current filters as"
                    value={templateName}
                    onChange={setTemplateName}
                    placeholder="template name"
                    width="w-56"
                  />
                  <Button onClick={saveTemplate}>Save template</Button>
                </>
              )}
              <ScreenerAwait state={templates} what="templates">
                {(list) =>
                  list.length === 0 ? (
                    <span className="text-[12px] text-[var(--text-dim)]">
                      No saved templates yet.
                    </span>
                  ) : (
                    <div className="flex flex-wrap gap-2">
                      {list.map((t) => (
                        <span
                          key={t.id}
                          className="flex items-center gap-1 rounded border border-[var(--border-strong)] px-2 py-1"
                        >
                          <button
                            type="button"
                            onClick={() => loadTemplate(t.filters)}
                            className="text-[12px] text-[var(--accent)] underline"
                          >
                            {t.name}
                          </button>
                          {mayConfig && (
                            <button
                              type="button"
                              onClick={() => deleteTemplate(t.id)}
                              aria-label={`Delete template ${t.name}`}
                              className="text-[12px] text-[var(--critical)]"
                            >
                              ✕
                            </button>
                          )}
                        </span>
                      ))}
                    </div>
                  )
                }
              </ScreenerAwait>
              {templateMsg && (
                <p
                  className={`text-[12px] ${templateMsg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
                >
                  {templateMsg.text}
                </p>
              )}
            </div>
          </>
        }
      >
        <div className="flex flex-wrap items-end gap-3">
          <TextFilterField
            label="Base asset/pair"
            value={basesAllowText}
            onChange={setBasesAllowText}
            placeholder="BTC"
            width="w-64"
            hint="One base symbol, or several comma-separated (BTC, ETH, SOL)."
          />
        </div>
        <div className="flex flex-wrap items-start gap-6">
          <FilterRow label="Buy on">
            <VenueChips
              selected={buyVenues}
              onToggle={(v) => setBuyVenues((prev) => toggleIn(prev, v))}
            />
          </FilterRow>
          <FilterRow label="Sell on">
            <VenueChips
              selected={sellVenues}
              onToggle={(v) => setSellVenues((prev) => toggleIn(prev, v))}
            />
          </FilterRow>
        </div>
        <div className="flex flex-wrap items-end gap-3">
          <SelectFilterField
            label="Quote asset"
            value={quote}
            onChange={setQuote}
            options={QUOTE_OPTIONS.map((q) => ({
              value: q,
              label: q || "any",
            }))}
          />
          <NumericFilterField
            label="Min net spread"
            value={minSpreadBps}
            onChange={setMinSpreadBps}
            unit="bps"
            width="w-28"
            hint="Filters on spread_bps_net (min_spread_bps), i.e. after taker fees."
          />
        </div>
      </FilterCard>

      <Section title="Spreads">
        <ScreenerAwait state={spreads} what="spreads">
          {() => (
            <VirtualTable
              head={head}
              align={align}
              // label names the horizontal scroll region so it is an
              // *accessible* region, not just a focusable div: at 1024px
              // and below the seven columns need internal scrolling, and
              // a region with no name tells a screen-reader user nothing
              // about what they have just tabbed into.
              label="Cross-exchange spreads"
              // rowKeys gives each row a stable identity across the 5s
              // poll. Without it React keys by array index, so a refresh
              // that reorders rows reuses a DOM row for a different
              // pair — moving focus and the open drawer's anchor onto a
              // candidate the user never selected.
              rowKeys={rows.map((r) => rowKey(r))}
              empty={`spreads matching these filters — 0 of ${allRows.length} pairs qualify`}
              rows={rows.map((r) => {
                const key = rowKey(r);
                const worstAgeMs = Math.max(r.buy_age_ms, r.sell_age_ms);
                // Stale book age uses the app-wide fixed 30s design
                // threshold (lib/format's bookAgeStaleMs — the same line
                // the FeedStale alert uses), not a poll-interval-relative
                // one, so "STALE" means the same thing everywhere it
                // appears (§A5).
                const stale = worstAgeMs > bookAgeStaleMs;
                const dim = staleCellClass(stale);
                const netClosed =
                  r.networks.buy_withdraw === "closed" ||
                  r.networks.sell_deposit === "closed";
                const netUnknown =
                  r.networks.buy_withdraw === "unknown" ||
                  r.networks.sell_deposit === "unknown";

                const cells: ReactNode[] = [
                  <span
                    key="p"
                    className={`inline-flex items-center gap-1.5 ${dim}`}
                  >
                    {r.base}/{r.quote}
                    {r.suspect && (
                      <span
                        title={
                          r.suspect_reason ??
                          "Same ticker, different asset (asset-identity guard)."
                        }
                      >
                        <Badge tone="warn">suspect</Badge>
                      </span>
                    )}
                  </span>,
                  <span key="b" className={dim}>
                    {r.buy_venue} @ <DecimalValue d={presentPrice(r.buy_ask)} />
                  </span>,
                  <span key="s" className={dim}>
                    {r.sell_venue} @{" "}
                    <DecimalValue d={presentPrice(r.sell_bid)} />
                  </span>,
                  <span key="n" className={`font-semibold ${dim}`}>
                    <DecimalValue
                      d={presentBps(r.spread_bps_net)}
                      tone="sign"
                    />
                  </span>,
                  <span key="l" className={dim}>
                    {r.liquidity_unknown ? (
                      <Badge tone="dim">unknown</Badge>
                    ) : (
                      <DecimalValue
                        d={presentQuote(r.liquidity_quote, r.quote)}
                      />
                    )}
                  </span>,
                ];
                if (showGrossColumn) {
                  cells.push(
                    <span key="g" className={dim}>
                      <DecimalValue
                        d={presentBps(r.spread_bps_gross)}
                        tone="sign"
                      />
                    </span>,
                  );
                }
                if (showLifetimeColumn) {
                  cells.push(
                    <span key="lt" className={dim}>
                      {r.lifetime_s}
                    </span>,
                  );
                }
                cells.push(
                  <span key="fr" className="flex items-center gap-1.5">
                    <span
                      className={
                        stale
                          ? "font-medium text-[var(--critical)]"
                          : "text-[var(--text-dim)]"
                      }
                    >
                      {r.buy_age_ms >= r.sell_age_ms ? "buy " : "sell "}
                      {bookAgeText(worstAgeMs)}
                    </span>
                    {/* One badge, worst status wins (closed over unknown
                        over open) — both sides' exact network state is
                        always in the drawer's Feasibility & limitations
                        section. */}
                    {netClosed && (
                      <NetworkBadge state="closed" reason={r.networks.reason} />
                    )}
                    {!netClosed && netUnknown && (
                      <NetworkBadge
                        state="unknown"
                        reason={r.networks.reason}
                      />
                    )}
                  </span>,
                  <button
                    key="x"
                    type="button"
                    onClick={(e) => {
                      expandTriggerRef.current = e.currentTarget;
                      setExpanded(key);
                    }}
                    className="rounded border border-[var(--border-strong)] px-2.5 py-1 text-[12px] font-medium text-[var(--text)] hover:bg-[var(--bg-raised)]"
                  >
                    Detail
                  </button>,
                );
                return cells;
              })}
            />
          )}
        </ScreenerAwait>
        <p className="mt-3 text-[12px] text-[var(--text-dim)]">
          {NO_TRANSFER_NOTE}
        </p>
      </Section>

      {expandedRow && (
        <RowDrawer
          title={`${expandedRow.base}/${expandedRow.quote}: ${expandedRow.buy_venue} → ${expandedRow.sell_venue}`}
          onClose={() => setExpanded(null)}
          returnFocusRef={expandTriggerRef}
          ageBadge={{
            label: bookAgeText(
              Math.max(
                expandedRow.buy_age_ms ?? 0,
                expandedRow.sell_age_ms ?? 0,
              ),
            ),
            tone:
              Math.max(
                expandedRow.buy_age_ms ?? 0,
                expandedRow.sell_age_ms ?? 0,
              ) > bookAgeStaleMs
                ? "bad"
                : "dim",
          }}
          footer={
            <>
              <Button onClick={() => setExpanded(null)}>Close</Button>
              <button
                type="button"
                onClick={() => openCalculator(expandedRow)}
                className="flex items-center gap-1 text-[13px] text-[var(--accent)] hover:underline"
              >
                Open in Calculator <ExternalIcon />
              </button>
            </>
          }
        >
          {!expandedRowPresent && (
            <div
              role="status"
              className="mb-4 rounded border border-[var(--warn)] bg-[var(--bg-panel)] p-3 text-[13px] text-[var(--warn)]"
            >
              No longer in the refreshed results — showing the last known
              values.
            </div>
          )}

          <Section title="Buy side">
            <dl className="grid min-w-0 grid-cols-[auto_1fr] gap-x-3 gap-y-2 text-[13px]">
              <dt className="text-[var(--text-dim)]">Venue</dt>
              <dd className="min-w-0 break-words text-right">
                {expandedRow.buy_venue}
              </dd>
              <dt className="text-[var(--text-dim)]">Ask</dt>
              <dd className="min-w-0 break-words text-right">
                <DecimalValue d={presentPrice(expandedRow.buy_ask)} />{" "}
                {expandedRow.quote}/{expandedRow.base}
              </dd>
              <dt className="text-[var(--text-dim)]">Ask qty</dt>
              <dd className="min-w-0 break-words text-right">
                <DecimalValue
                  d={presentQty(expandedRow.buy_ask_qty, expandedRow.base)}
                />
              </dd>
              <dt className="text-[var(--text-dim)]">Taker fee</dt>
              <dd className="min-w-0 break-words text-right">
                <DecimalValue d={presentFeeBps(expandedRow.buy_fee_bps)} />
              </dd>
              <dt className="text-[var(--text-dim)]">Age</dt>
              <dd className="min-w-0 break-words text-right">
                {bookAgeText(expandedRow.buy_age_ms)}
              </dd>
            </dl>
          </Section>
          <Section title="Sell side">
            <dl className="grid min-w-0 grid-cols-[auto_1fr] gap-x-3 gap-y-2 text-[13px]">
              <dt className="text-[var(--text-dim)]">Venue</dt>
              <dd className="min-w-0 break-words text-right">
                {expandedRow.sell_venue}
              </dd>
              <dt className="text-[var(--text-dim)]">Bid</dt>
              <dd className="min-w-0 break-words text-right">
                <DecimalValue d={presentPrice(expandedRow.sell_bid)} />{" "}
                {expandedRow.quote}/{expandedRow.base}
              </dd>
              <dt className="text-[var(--text-dim)]">Bid qty</dt>
              <dd className="min-w-0 break-words text-right">
                <DecimalValue
                  d={presentQty(expandedRow.sell_bid_qty, expandedRow.base)}
                />
              </dd>
              <dt className="text-[var(--text-dim)]">Taker fee</dt>
              <dd className="min-w-0 break-words text-right">
                <DecimalValue d={presentFeeBps(expandedRow.sell_fee_bps)} />
              </dd>
              <dt className="text-[var(--text-dim)]">Age</dt>
              <dd className="min-w-0 break-words text-right">
                {bookAgeText(expandedRow.sell_age_ms)}
              </dd>
            </dl>
          </Section>
          <Section title="Costs">
            <dl className="grid min-w-0 grid-cols-[auto_1fr] gap-x-3 gap-y-2 text-[13px]">
              <dt className="text-[var(--text-dim)]">Gross bps</dt>
              <dd className="min-w-0 break-words text-right">
                <DecimalValue
                  d={presentBps(expandedRow.spread_bps_gross)}
                  tone="sign"
                />
              </dd>
              <dt className="text-[var(--text-dim)]">Net bps</dt>
              <dd className="min-w-0 break-words text-right">
                <DecimalValue
                  d={presentBps(expandedRow.spread_bps_net)}
                  tone="sign"
                />
              </dd>
            </dl>
            <p className="mt-2 text-[12px] text-[var(--text-dim)]">
              {NO_TRANSFER_NOTE}
            </p>
          </Section>
          <Section title="Feasibility & limitations">
            <dl className="grid min-w-0 grid-cols-[auto_1fr] gap-x-3 gap-y-2 text-[13px]">
              <dt className="text-[var(--text-dim)]">Liquidity</dt>
              <dd className="min-w-0 break-words text-right">
                {expandedRow.liquidity_unknown ? (
                  <Badge tone="dim">unknown</Badge>
                ) : (
                  <DecimalValue
                    d={presentQuote(
                      expandedRow.liquidity_quote,
                      expandedRow.quote,
                    )}
                  />
                )}
              </dd>
              <dt className="text-[var(--text-dim)]">Asset identity</dt>
              <dd className="min-w-0 break-words text-right">
                {expandedRow.suspect ? (
                  <span title={expandedRow.suspect_reason}>
                    <Badge tone="warn">suspect</Badge>
                  </span>
                ) : (
                  <Badge tone="dim">not flagged</Badge>
                )}
              </dd>
              <dt className="text-[var(--text-dim)]">Buy-side network</dt>
              <dd className="min-w-0 break-words text-right">
                <NetworkBadge
                  state={expandedRow.networks.buy_withdraw}
                  reason={expandedRow.networks.reason}
                />
              </dd>
              <dt className="text-[var(--text-dim)]">Sell-side network</dt>
              <dd className="min-w-0 break-words text-right">
                <NetworkBadge
                  state={expandedRow.networks.sell_deposit}
                  reason={expandedRow.networks.reason}
                />
              </dd>
            </dl>
          </Section>
          <Section title="Identity & age">
            <dl className="grid min-w-0 grid-cols-[auto_1fr] gap-x-3 gap-y-2 text-[13px]">
              <dt className="text-[var(--text-dim)]">First seen</dt>
              <dd className="min-w-0 break-words text-right">
                {fmtTime(expandedRow.first_seen_at)}
              </dd>
              <dt className="text-[var(--text-dim)]">Lifetime</dt>
              <dd className="min-w-0 break-words text-right">
                {expandedRow.lifetime_s}s
              </dd>
            </dl>
          </Section>
        </RowDrawer>
      )}
    </ConsoleShell>
  );
}

export default function ScreenerPage() {
  return (
    <Suspense fallback={<Loading what="screener" />}>
      <ScreenerPageInner />
    </Suspense>
  );
}
