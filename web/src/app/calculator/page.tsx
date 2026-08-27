"use client";

// Spreads calculator (design §5/§7 POST /screener/calculator): enter
// size, venues, fees, transfer fee → the backend's own net result. The
// Screener page's row-expand "Calculate…" prefills base/quote/venues via
// query params — reading them needs useSearchParams, hence the Suspense
// wrapper (Next.js static-bailout requirement for that hook).

import { Suspense, useState } from "react";
import { useSearchParams } from "next/navigation";
import { api, ApiError, type ScreenerCalculatorResult } from "@/lib/api/client";
import { ConsoleShell } from "@/components/ConsoleShell";
import { VENUE_OPTIONS } from "@/components/screener/ScreenerShared";
import { Button, Loading, PageTitle, Section, Stat } from "@/components/ui";

type ResultState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "ready"; result: ScreenerCalculatorResult };

function CalculatorPageInner() {
  const params = useSearchParams();

  const [base, setBase] = useState(params.get("base") ?? "");
  const [quote, setQuote] = useState(params.get("quote") ?? "USDT");
  const [buyVenue, setBuyVenue] = useState(
    params.get("buy_venue") ?? VENUE_OPTIONS[0]!,
  );
  const [sellVenue, setSellVenue] = useState(
    params.get("sell_venue") ?? VENUE_OPTIONS[1]!,
  );
  const [sizeQuote, setSizeQuote] = useState(params.get("size") ?? "1000");
  const [transferFee, setTransferFee] = useState("");
  const [overrideBuyFee, setOverrideBuyFee] = useState("");
  const [overrideSellFee, setOverrideSellFee] = useState("");

  const [state, setState] = useState<ResultState>({ kind: "idle" });

  const submit = async () => {
    if (!base.trim() || !quote.trim() || !sizeQuote.trim()) {
      setState({
        kind: "error",
        message: "Base, quote and size are required.",
      });
      return;
    }
    setState({ kind: "loading" });
    try {
      const result = await api.screener.calculator({
        base: base.trim().toUpperCase(),
        quote: quote.trim().toUpperCase(),
        buy_venue: buyVenue,
        sell_venue: sellVenue,
        size_quote: sizeQuote.trim(),
        transfer_fee_quote: transferFee.trim() || undefined,
        override_fees:
          overrideBuyFee.trim() || overrideSellFee.trim()
            ? {
                buy_fee_bps: overrideBuyFee.trim() || undefined,
                sell_fee_bps: overrideSellFee.trim() || undefined,
              }
            : undefined,
      });
      setState({ kind: "ready", result });
    } catch (err: unknown) {
      if (
        err instanceof ApiError &&
        (err.status === 404 || err.status === 503)
      ) {
        setState({
          kind: "error",
          message: "Screener backend not available in this build.",
        });
        return;
      }
      setState({
        kind: "error",
        message: err instanceof ApiError ? err.message : "Calculation failed.",
      });
    }
  };

  return (
    <ConsoleShell active="Calculator">
      <PageTitle>Spreads calculator</PageTitle>

      <Section title="Inputs">
        <div className="max-w-xl space-y-3 text-[13px]">
          <div className="grid grid-cols-2 gap-3">
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Base asset
              </span>
              <input
                value={base}
                onChange={(e) => setBase(e.target.value)}
                placeholder="BTC"
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Quote asset
              </span>
              <input
                value={quote}
                onChange={(e) => setQuote(e.target.value)}
                placeholder="USDT"
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Buy venue
              </span>
              <select
                value={buyVenue}
                onChange={(e) => setBuyVenue(e.target.value)}
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none"
              >
                {VENUE_OPTIONS.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Sell venue
              </span>
              <select
                value={sellVenue}
                onChange={(e) => setSellVenue(e.target.value)}
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none"
              >
                {VENUE_OPTIONS.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Size (quote)
              </span>
              <input
                value={sizeQuote}
                onChange={(e) => setSizeQuote(e.target.value)}
                inputMode="decimal"
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Transfer fee (quote, optional)
              </span>
              <input
                value={transferFee}
                onChange={(e) => setTransferFee(e.target.value)}
                inputMode="decimal"
                placeholder="0 (no-transfer model)"
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Override buy fee (bps, optional)
              </span>
              <input
                value={overrideBuyFee}
                onChange={(e) => setOverrideBuyFee(e.target.value)}
                inputMode="decimal"
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Override sell fee (bps, optional)
              </span>
              <input
                value={overrideSellFee}
                onChange={(e) => setOverrideSellFee(e.target.value)}
                inputMode="decimal"
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
          </div>
          <Button onClick={submit} disabled={state.kind === "loading"}>
            {state.kind === "loading" ? "Calculating…" : "Calculate"}
          </Button>
          {state.kind === "error" && (
            <p className="text-[13px] text-[var(--critical)]">
              {state.message}
            </p>
          )}
        </div>
      </Section>

      {state.kind === "ready" && (
        <Section title="Result">
          <div className="grid max-w-3xl grid-cols-2 gap-3 sm:grid-cols-4">
            <Stat label="Buy ask" value={state.result.buy_ask} />
            <Stat label="Sell bid" value={state.result.sell_bid} />
            <Stat label="Size (base)" value={state.result.size_base} />
            <Stat label="Gross" value={state.result.gross} />
            <Stat label="Buy fees" value={state.result.fees_buy} />
            <Stat label="Sell fees" value={state.result.fees_sell} />
            <Stat label="Transfer fee" value={state.result.transfer_fee} />
            <Stat
              label="Net"
              value={state.result.net}
              tone={state.result.net.trim().startsWith("-") ? "bad" : "ok"}
            />
            <Stat
              label="Net bps"
              value={state.result.net_bps}
              tone={state.result.net_bps.trim().startsWith("-") ? "bad" : "ok"}
            />
            <Stat
              label="Liquidity"
              value={state.result.liquidity_ok ? "sufficient" : "insufficient"}
              tone={state.result.liquidity_ok ? "ok" : "warn"}
            />
          </div>
        </Section>
      )}
    </ConsoleShell>
  );
}

export default function CalculatorPage() {
  return (
    <Suspense fallback={<Loading what="calculator" />}>
      <CalculatorPageInner />
    </Suspense>
  );
}
