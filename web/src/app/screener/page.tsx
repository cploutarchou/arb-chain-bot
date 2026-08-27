"use client";

// Cross-venue spot screener (design §5/§7 GET /screener/spreads): stats
// strip, filter card with saved templates, a dense auto-refreshing
// table, and a row-expand detail panel (never an inline table row — that
// would break VirtualTable's fixed-row-height windowing above 500 rows).

import { useMemo, useState } from "react";
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
  NetworkBadge,
  ScreenerAwait,
  VenueChips,
  fmtAge,
  isStaleAge,
  parseCsv,
  pollIntervalSFromStatus,
  pollMsFromStatus,
  signTone,
  useScreenerStatus,
} from "@/components/screener/ScreenerShared";
import {
  Button,
  PageTitle,
  Section,
  Stat,
  VirtualTable,
  fmtTime,
} from "@/components/ui";

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
  const [expanded, setExpanded] = useState<string | null>(null);

  const basesAllow = useMemo(() => parseCsv(basesAllowText), [basesAllowText]);
  const basesDeny = useMemo(() => parseCsv(basesDenyText), [basesDenyText]);

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
    <ConsoleShell active="Screener">
      <PageTitle>Screener</PageTitle>

      <ScreenerAwait state={status} what="screener status">
        {(s) => {
          const online = (s.venues ?? []).filter((v) => v.online).length;
          const total = (s.venues ?? []).length;
          const ageMs = Date.now() - new Date(s.updated_at).getTime();
          return (
            <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Stat
                label="Venues online"
                value={`${online} / ${total}`}
                tone={online === total && total > 0 ? "ok" : "warn"}
              />
              <Stat label="Pairs tracked" value={s.pairs_tracked} />
              <Stat label="Spreads / sec" value={s.spreads_per_sec} />
              <Stat label="Data age" value={fmtAge(Math.max(0, ageMs))} />
            </div>
          );
        }}
      </ScreenerAwait>

      <Section title="Filters">
        <div className="max-w-4xl space-y-3 text-[13px]">
          <div className="flex flex-wrap items-start gap-6">
            <div>
              <div className="mb-1 text-[12px] text-[var(--text-dim)]">
                Buy venues
              </div>
              <VenueChips
                selected={buyVenues}
                onToggle={(v) => setBuyVenues((prev) => toggleIn(prev, v))}
              />
            </div>
            <div>
              <div className="mb-1 text-[12px] text-[var(--text-dim)]">
                Sell venues
              </div>
              <VenueChips
                selected={sellVenues}
                onToggle={(v) => setSellVenues((prev) => toggleIn(prev, v))}
              />
            </div>
          </div>
          <div className="flex flex-wrap items-end gap-3">
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Min spread (bps)
              </span>
              <input
                value={minSpreadBps}
                onChange={(e) => setMinSpreadBps(e.target.value)}
                inputMode="decimal"
                className="w-28 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Min liquidity (quote)
              </span>
              <input
                value={minLiquidity}
                onChange={(e) => setMinLiquidity(e.target.value)}
                inputMode="decimal"
                className="w-32 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Min lifetime (s)
              </span>
              <input
                value={minLifetimeS}
                onChange={(e) => setMinLifetimeS(e.target.value)}
                inputMode="numeric"
                className="w-24 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Quote asset
              </span>
              <select
                value={quote}
                onChange={(e) => setQuote(e.target.value)}
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none"
              >
                {QUOTE_OPTIONS.map((q) => (
                  <option key={q || "any"} value={q}>
                    {q || "any"}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div className="flex flex-wrap items-end gap-3">
            <label className="flex flex-1 min-w-[220px] flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Base allow-list (comma-separated)
              </span>
              <input
                value={basesAllowText}
                onChange={(e) => setBasesAllowText(e.target.value)}
                placeholder="BTC, ETH, SOL"
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-1 min-w-[220px] flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Base deny-list (comma-separated)
              </span>
              <input
                value={basesDenyText}
                onChange={(e) => setBasesDenyText(e.target.value)}
                placeholder="SHIB, PEPE"
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
          </div>

          {/* Templates are a per-user read (§7: "GET /screener/templates
              ... (per user)") — VIEWER/OPERATOR can load their own saved
              filters same as ADMIN; only saving a new one and deleting
              are mutations gated behind screener:config. */}
          <div className="flex flex-wrap items-end gap-3 border-t border-[var(--border)] pt-3">
            {mayConfig && (
              <>
                <label className="flex flex-col gap-1">
                  <span className="text-[12px] text-[var(--text-dim)]">
                    Save current filters as
                  </span>
                  <input
                    value={templateName}
                    onChange={(e) => setTemplateName(e.target.value)}
                    placeholder="template name"
                    className="w-56 rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
                  />
                </label>
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
                        className="flex items-center gap-1 rounded border border-[var(--border)] px-2 py-1"
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
        </div>
      </Section>

      <Section title="Spreads">
        <ScreenerAwait state={spreads} what="spreads">
          {() => (
            <VirtualTable
              head={[
                "Pair",
                "Buy venue / ask",
                "Sell venue / bid",
                "Gross bps",
                "Net bps",
                "Liquidity (quote)",
                "Lifetime (s)",
                "Networks",
                "Buy age",
                "Sell age",
                "",
              ]}
              empty="spreads matching these filters"
              rows={rows.map((r) => {
                const key = rowKey(r);
                const stale =
                  isStaleAge(r.buy_age_ms, pollIntervalS) ||
                  isStaleAge(r.sell_age_ms, pollIntervalS);
                const netTone = signTone(r.spread_bps_net);
                return [
                  <span
                    key="p"
                    className={stale ? "text-[var(--text-dim)]" : undefined}
                  >
                    {r.base}/{r.quote}
                  </span>,
                  <span key="b">
                    {r.buy_venue} @ {r.buy_ask}
                  </span>,
                  <span key="s">
                    {r.sell_venue} @ {r.sell_bid}
                  </span>,
                  r.spread_bps_gross,
                  <span
                    key="n"
                    className={`font-semibold ${netTone === "ok" ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
                  >
                    {r.spread_bps_net}
                  </span>,
                  r.liquidity_quote,
                  r.lifetime_s,
                  <span key="net" className="flex gap-1">
                    <NetworkBadge
                      state={r.networks.buy_withdraw}
                      reason={r.networks.reason}
                    />
                    <NetworkBadge
                      state={r.networks.sell_deposit}
                      reason={r.networks.reason}
                    />
                  </span>,
                  fmtAge(r.buy_age_ms),
                  fmtAge(r.sell_age_ms),
                  <Button
                    key="x"
                    onClick={() => setExpanded(expanded === key ? null : key)}
                  >
                    {expanded === key ? "Hide" : "Expand"}
                  </Button>,
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
        <Section
          title={`${expandedRow.base}/${expandedRow.quote}: ${expandedRow.buy_venue} → ${expandedRow.sell_venue}`}
        >
          <div className="grid max-w-3xl grid-cols-2 gap-3 sm:grid-cols-3">
            <Stat
              label={`${expandedRow.buy_venue} ask × qty`}
              value={`${expandedRow.buy_ask} × ${expandedRow.buy_ask_qty}`}
            />
            <Stat
              label={`${expandedRow.sell_venue} bid × qty`}
              value={`${expandedRow.sell_bid} × ${expandedRow.sell_bid_qty}`}
            />
            <Stat
              label="Liquidity (quote)"
              value={expandedRow.liquidity_quote}
            />
            <Stat
              label="Buy taker fee"
              value={`${expandedRow.buy_fee_bps} bps`}
            />
            <Stat
              label="Sell taker fee"
              value={`${expandedRow.sell_fee_bps} bps`}
            />
            <Stat
              label="Gross / Net"
              value={`${expandedRow.spread_bps_gross} / ${expandedRow.spread_bps_net} bps`}
            />
            <Stat
              label="First seen"
              value={fmtTime(expandedRow.first_seen_at)}
            />
            <Stat label="Lifetime" value={`${expandedRow.lifetime_s}s`} />
          </div>
          <div className="mt-3">
            <Button onClick={() => openCalculator(expandedRow)}>
              Calculate…
            </Button>
          </div>
        </Section>
      )}
    </ConsoleShell>
  );
}
