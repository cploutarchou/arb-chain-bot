"use client";

// Cross-venue spot screener (design §5/§7 GET /screener/spreads): stats
// strip, filter card with saved templates, a dense auto-refreshing
// table, and a row-expand detail drawer (never an inline table row —
// that would break VirtualTable's fixed-row-height windowing above 500
// rows; design-system.md §4.3 / UX §4).

import { useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  api,
  ApiError,
  type ScreenerFilterSet,
  type ScreenerSpreadRow,
} from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  NO_TRANSFER_NOTE,
  ScreenerAwait,
  VenueChips,
  ageCellText,
  ageTone,
  fmtAge,
  isStaleAge,
  NetworkBadge,
  parseCsv,
  pollIntervalSFromStatus,
  pollMsFromStatus,
  signTone,
  signedText,
  staleCellClass,
  useScreenerStatus,
} from "@/components/screener/ScreenerShared";
import { fmtDecimal } from "@/lib/decimal";
import {
  Button,
  PageTitle,
  Section,
  Stat,
  VirtualTable,
  fmtTime,
} from "@/components/ui";
import {
  FilterCard,
  FilterRow,
  NumericFilterField,
  SelectFilterField,
  TextFilterField,
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

export default function ScreenerPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayConfig = can(role, "screener:config");
  const router = useRouter();

  const status = useScreenerStatus();
  const pollMs = pollMsFromStatus(status);
  const pollIntervalS = pollIntervalSFromStatus(status);

  const [buyVenues, setBuyVenues] = useState<string[]>([]);
  const [sellVenues, setSellVenues] = useState<string[]>([]);
  const [quote, setQuote] = useState("");
  const [minSpreadBps, setMinSpreadBps] = useState("");
  const [minLiquidity, setMinLiquidity] = useState("");
  const [minLifetimeS, setMinLifetimeS] = useState("");
  const [basesAllowText, setBasesAllowText] = useState("");
  const [basesDenyText, setBasesDenyText] = useState("");
  // Both OFF by default, matching the backend's own safe default
  // (handleScreenerSpreads excludes suspect/unknown-liquidity lanes
  // unless explicitly opted in).
  const [includeSuspect, setIncludeSuspect] = useState(false);
  const [includeUnknownLiquidity, setIncludeUnknownLiquidity] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
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

  // clearFilters resets every filter to the safe defaults (audit §4C:
  // "an obvious clear action" beside the applied-filter count the
  // FilterCard already carries).
  const clearFilters = () => {
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
  const expandedRow = rows.find((r) => rowKey(r) === expanded);

  const openCalculator = (r: ScreenerSpreadRow) => {
    const p = new URLSearchParams({
      base: r.base,
      quote: r.quote,
      buy_venue: r.buy_venue,
      sell_venue: r.sell_venue,
    });
    router.push(`/calculator?${p.toString()}`);
  };

  return (
    <ConsoleShell>
      <PageTitle>Screener</PageTitle>

      <ScreenerAwait state={status} what="screener status">
        {(s) => {
          const online = (s.venues ?? []).filter((v) => v.online).length;
          const total = (s.venues ?? []).length;
          const ageMs = Date.now() - new Date(s.updated_at).getTime();
          return (
            <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-5">
              <Stat
                label="Venues online"
                value={`${online} / ${total}`}
                tone={online === total && total > 0 ? "ok" : "warn"}
              />
              <Stat label="Pairs tracked" value={s.pairs_tracked} />
              <Stat label="Spreads / sec" value={s.spreads_per_sec} />
              <Stat label="Data age" value={fmtAge(Math.max(0, ageMs))} />
              <Stat label="Poll interval" value={`${pollIntervalS}s`} />
            </div>
          );
        }}
      </ScreenerAwait>

      {/* Excluded counts come from the spreads response, not the status
          poll above — always reported by handleScreenerSpreads regardless
          of whether either toggle is on, so the operator can see how many
          lanes the safe defaults hid even before opting in. One compact
          line, not a second counter row (client-area audit §2). */}
      {spreads.kind === "ready" && spreads.data.excluded && (
        <p className="mb-4 text-[12px] text-[var(--text-dim)]">
          Excluded: suspect {spreads.data.excluded.suspect} · unknown
          liquidity {spreads.data.excluded.liquidity_unknown} — opt in under
          Advanced filters below.
        </p>
      )}

      <FilterCard activeCount={activeFilterCount}>
        {/* Common filters first (client-area audit §2): venues, the
            accurately-named net-spread threshold and the quote asset do
            most of the narrowing; everything else folds into the
            Advanced disclosure below. */}
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
          <NumericFilterField
            label="Min net spread"
            value={minSpreadBps}
            onChange={setMinSpreadBps}
            unit="bps"
            width="w-28"
          />
          <SelectFilterField
            label="Quote asset"
            value={quote}
            onChange={setQuote}
            options={QUOTE_OPTIONS.map((q) => ({
              value: q,
              label: q || "any",
            }))}
          />
          <Button
            onClick={clearFilters}
            disabled={activeFilterCount === 0}
          >
            Clear filters
          </Button>
        </div>
        {/* Advanced disclosure (client-area audit §2): allow/deny lists,
            lifetime and liquidity floors, and the unsafe-lane opt-ins are
            the less common controls — collapsed by default, opened in one
            activation. */}
        <details>
          <summary className="cursor-pointer select-none text-[12px] font-medium text-[var(--text-dim)] hover:text-[var(--text)]">
            Advanced filters
            {(minLiquidity.trim() !== "" ||
              minLifetimeS.trim() !== "" ||
              basesAllowText.trim() !== "" ||
              basesDenyText.trim() !== "" ||
              includeSuspect ||
              includeUnknownLiquidity) && (
              <span className="ml-2 rounded border border-[var(--border-strong)] px-1.5 py-0.5 text-[11px]">
                {
                  [
                    minLiquidity.trim() !== "",
                    minLifetimeS.trim() !== "",
                    basesAllowText.trim() !== "",
                    basesDenyText.trim() !== "",
                    includeSuspect,
                    includeUnknownLiquidity,
                  ].filter(Boolean).length
                }{" "}
                active
              </span>
            )}
          </summary>
          <div className="mt-3 space-y-3">
            <div className="flex flex-wrap items-end gap-3">
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
            <div className="flex flex-wrap items-end gap-3">
              <TextFilterField
                label="Base allow-list (comma-separated)"
                value={basesAllowText}
                onChange={setBasesAllowText}
                placeholder="BTC, ETH, SOL"
                width="flex-1 min-w-[220px]"
              />
              <TextFilterField
                label="Base deny-list (comma-separated)"
                value={basesDenyText}
                onChange={setBasesDenyText}
                placeholder="SHIB, PEPE"
                width="flex-1 min-w-[220px]"
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
                  onChange={(e) => setIncludeUnknownLiquidity(e.target.checked)}
                />
                Include unknown-liquidity lanes
              </label>
            </div>
          </div>
        </details>

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
      </FilterCard>

      <Section title="Spreads">
        <ScreenerAwait state={spreads} what="spreads">
          {() => (
            <VirtualTable
              head={[
                "Pair",
                "Route (buy → sell)",
                "Net bps",
                "Gross bps",
                "Liquidity (quote)",
                "Freshness",
                "",
              ]}
              align={["text", "text", "num", "num", "num", "text", "text"]}
              empty={`spreads matching these filters — 0 of ${allRows.length} pairs qualify`}
              rows={rows.map((r) => {
                const key = rowKey(r);
                const stale =
                  isStaleAge(r.buy_age_ms, pollIntervalS) ||
                  isStaleAge(r.sell_age_ms, pollIntervalS);
                const dim = staleCellClass(stale);
                const netTone = signTone(r.spread_bps_net);
                // Freshness is the worse of the two legs' ages — the
                // binding constraint on the row (per-leg ages and prices
                // stay one Detail away, audit §2: fewer default columns,
                // decision-relevant ones first).
                const worstAgeMs = Math.max(
                  r.buy_age_ms ?? 0,
                  r.sell_age_ms ?? 0,
                );
                const worstTone = ageTone(worstAgeMs, pollIntervalS);
                return [
                  <span key="p" className={dim}>
                    {r.base}/{r.quote}
                  </span>,
                  <span
                    key="r"
                    className={dim}
                    title={`${r.buy_venue} ask ${r.buy_ask} → ${r.sell_venue} bid ${r.sell_bid}`}
                  >
                    {r.buy_venue} → {r.sell_venue}
                  </span>,
                  <span
                    key="n"
                    title={r.spread_bps_net}
                    className={`font-semibold ${dim} ${netTone === "ok" ? "text-[var(--pos)]" : "text-[var(--neg)]"}`}
                  >
                    {signedText(fmtDecimal(r.spread_bps_net, { maxFrac: 2 }))}
                  </span>,
                  <span key="g" className={dim} title={r.spread_bps_gross}>
                    {fmtDecimal(r.spread_bps_gross, { maxFrac: 2 })}
                  </span>,
                  <span key="l" className={dim} title={r.liquidity_quote ?? undefined}>
                    {r.liquidity_quote ? fmtDecimal(r.liquidity_quote, { maxFrac: 2 }) : "unknown"}
                  </span>,
                  <span
                    key="f"
                    title={`buy ${ageCellText(r.buy_age_ms, pollIntervalS)} · sell ${ageCellText(r.sell_age_ms, pollIntervalS)} · lifetime ${r.lifetime_s}s · networks ${r.networks.buy_withdraw}/${r.networks.sell_deposit}${r.networks.reason ? ` (${r.networks.reason})` : ""}`}
                    className={
                      worstTone === "bad"
                        ? "text-[var(--critical)]"
                        : worstTone === "warn"
                          ? "text-[var(--warn)]"
                          : "text-[var(--text-dim)]"
                    }
                  >
                    {ageCellText(worstAgeMs, pollIntervalS)}
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
                ];
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
            label: ageCellText(
              Math.max(
                expandedRow.buy_age_ms ?? 0,
                expandedRow.sell_age_ms ?? 0,
              ),
              pollIntervalS,
            ),
            tone: ageTone(
              Math.max(
                expandedRow.buy_age_ms ?? 0,
                expandedRow.sell_age_ms ?? 0,
              ),
              pollIntervalS,
            ),
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
          <Section title="Buy side">
            <dl className="grid grid-cols-2 gap-y-2 text-[13px]">
              <dt className="text-[var(--text-dim)]">Venue</dt>
              <dd className="text-right">{expandedRow.buy_venue}</dd>
              <dt className="text-[var(--text-dim)]">Ask × qty</dt>
              <dd className="text-right" title={`${expandedRow.buy_ask} × ${expandedRow.buy_ask_qty}`}>
                {fmtDecimal(expandedRow.buy_ask, { maxFrac: 8 })} ×{" "}
                {fmtDecimal(expandedRow.buy_ask_qty, { maxFrac: 8 })}
              </dd>
              <dt className="text-[var(--text-dim)]">Taker fee</dt>
              <dd className="text-right">{expandedRow.buy_fee_bps} bps</dd>
              <dt className="text-[var(--text-dim)]">Age</dt>
              <dd className="text-right">
                {ageCellText(expandedRow.buy_age_ms, pollIntervalS)}
              </dd>
            </dl>
          </Section>
          <Section title="Sell side">
            <dl className="grid grid-cols-2 gap-y-2 text-[13px]">
              <dt className="text-[var(--text-dim)]">Venue</dt>
              <dd className="text-right">{expandedRow.sell_venue}</dd>
              <dt className="text-[var(--text-dim)]">Bid × qty</dt>
              <dd className="text-right" title={`${expandedRow.sell_bid} × ${expandedRow.sell_bid_qty}`}>
                {fmtDecimal(expandedRow.sell_bid, { maxFrac: 8 })} ×{" "}
                {fmtDecimal(expandedRow.sell_bid_qty, { maxFrac: 8 })}
              </dd>
              <dt className="text-[var(--text-dim)]">Taker fee</dt>
              <dd className="text-right">{expandedRow.sell_fee_bps} bps</dd>
              <dt className="text-[var(--text-dim)]">Age</dt>
              <dd className="text-right">
                {ageCellText(expandedRow.sell_age_ms, pollIntervalS)}
              </dd>
            </dl>
          </Section>
          <Section title="Calculator">
            <dl className="grid grid-cols-2 gap-y-2 text-[13px]">
              <dt className="text-[var(--text-dim)]">Liquidity (quote)</dt>
              <dd
                className="text-right"
                title={expandedRow.liquidity_quote ?? undefined}
              >
                {expandedRow.liquidity_quote
                  ? fmtDecimal(expandedRow.liquidity_quote, { maxFrac: 2 })
                  : "unknown"}
              </dd>
              <dt className="text-[var(--text-dim)]">Gross / Net (bps)</dt>
              <dd
                className="text-right"
                title={`${expandedRow.spread_bps_gross} / ${expandedRow.spread_bps_net}`}
              >
                {fmtDecimal(expandedRow.spread_bps_gross, { maxFrac: 2 })} /{" "}
                {signedText(fmtDecimal(expandedRow.spread_bps_net, { maxFrac: 2 }))}
              </dd>
              <dt className="text-[var(--text-dim)]">First seen</dt>
              <dd className="text-right">
                {fmtTime(expandedRow.first_seen_at)}
              </dd>
              <dt className="text-[var(--text-dim)]">Lifetime</dt>
              <dd className="text-right">{expandedRow.lifetime_s}s</dd>
              <dt className="text-[var(--text-dim)]">Networks</dt>
              <dd
                className="text-right"
                title={expandedRow.networks.reason || undefined}
              >
                <NetworkBadge
                  state={expandedRow.networks.buy_withdraw}
                  reason={expandedRow.networks.reason}
                />{" "}
                <NetworkBadge
                  state={expandedRow.networks.sell_deposit}
                  reason={expandedRow.networks.reason}
                />
              </dd>
            </dl>
          </Section>
        </RowDrawer>
      )}
    </ConsoleShell>
  );
}
