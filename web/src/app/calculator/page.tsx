"use client";

// Spreads calculator (design §5/§7 POST /screener/calculator): enter
// size, venues, fees, transfer fee → the backend's own net result. The
// Screener page's row-expand "Open in Calculator" prefills base/quote/
// venues/size via query params — reading them needs useSearchParams,
// hence the Suspense wrapper (Next.js static-bailout requirement for
// that hook).
//
// T-087 §C redesign (docs/design/client-area-audit-2026-09-12 §4): the
// audit's central finding was a green Net/Net bps rendered directly
// above a "Liquidity: insufficient" card — a result that looks
// profitable next to a caveat that says it cannot be executed. This
// page now renders the backend's only feasibility signal
// (`liquidity_ok`) as a single plain-worded block *before* the numeric
// estimate, and only colours Net/Net bps "ok" (green) when liquidity_ok
// is true — a genuinely negative net still reads "bad" either way, but a
// non-negative one never reads "ok" while the trade is not executable at
// this size. No returned number is changed by this — only how it is
// coloured and where it sits on the page.

import { Suspense, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import Link from "next/link";
import {
  api,
  ApiError,
  type ScreenerCalculatorResult,
} from "@/lib/api/client";
import {
  cmpDecimalStr,
  presentBps,
  presentPrice,
  presentQty,
  presentQuote,
  presentSignedQuote,
  type DecimalDisplay,
} from "@/lib/decimal";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  NO_TRANSFER_NOTE,
  useScreenerStatus,
  VENUE_OPTIONS,
} from "@/components/screener/ScreenerShared";
import {
  Button,
  DecimalValue,
  Loading,
  PageTitle,
  Section,
  type Tone,
} from "@/components/ui";
import { InfoIcon, PlusMinusIcon, WarnTriIcon } from "@/components/icons";

interface CalculatorRequestContext {
  base: string;
  quote: string;
  buyVenue: string;
  sellVenue: string;
}

type ResultState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "error"; message: string }
  // The request context travels with the result so labels always match
  // the numbers on screen, even if the operator edits the form afterwards
  // without recalculating.
  | {
      kind: "ready";
      result: ScreenerCalculatorResult;
      request: CalculatorRequestContext;
    };

interface FieldErrors {
  base?: string;
  quote?: string;
  size?: string;
  transferFee?: string;
  overrideBuyFee?: string;
  overrideSellFee?: string;
}

const DECIMAL_RE = /^\d+(\.\d+)?$/;

function isPositiveDecimal(v: string): boolean {
  const t = v.trim();
  return DECIMAL_RE.test(t) && cmpDecimalStr(t, "0") > 0;
}

function isNonNegativeDecimalOrEmpty(v: string): boolean {
  const t = v.trim();
  if (t === "") return true;
  return DECIMAL_RE.test(t) && cmpDecimalStr(t, "0") >= 0;
}

function validate(fields: {
  base: string;
  quote: string;
  sizeQuote: string;
  transferFee: string;
  overrideBuyFee: string;
  overrideSellFee: string;
}): FieldErrors {
  const errs: FieldErrors = {};
  if (!fields.base.trim()) errs.base = "Base asset is required.";
  if (!fields.quote.trim()) errs.quote = "Quote asset is required.";
  if (!isPositiveDecimal(fields.sizeQuote))
    errs.size = "Enter a size greater than 0.";
  if (!isNonNegativeDecimalOrEmpty(fields.transferFee))
    errs.transferFee = "Enter 0 or greater, or leave blank.";
  if (!isNonNegativeDecimalOrEmpty(fields.overrideBuyFee))
    errs.overrideBuyFee = "Enter 0 or greater, or leave blank.";
  if (!isNonNegativeDecimalOrEmpty(fields.overrideSellFee))
    errs.overrideSellFee = "Enter 0 or greater, or leave blank.";
  return errs;
}

// feasibilityTone keeps a genuinely negative net "bad" regardless, but
// only ever shows "ok" (green) when the backend's own liquidity_ok says
// this size is actually covered by top-of-book — the exact defect the
// audit named (a green Net next to "Liquidity: insufficient"). This is a
// presentation choice about colour only: the sign is still in the text
// either way (DecimalValue never relies on colour alone).
function feasibilityTone(d: DecimalDisplay, liquidityOk: boolean): Tone {
  if (!d.valid || d.zero) return "dim";
  if (d.negative) return "bad";
  return liquidityOk ? "ok" : "dim";
}

function backToScreenerHref(ctx: {
  base: string;
  quote: string;
  buyVenue: string;
  sellVenue: string;
}): string {
  const p = new URLSearchParams();
  if (ctx.base.trim()) p.set("base", ctx.base.trim().toUpperCase());
  if (ctx.quote.trim()) p.set("quote", ctx.quote.trim().toUpperCase());
  if (ctx.buyVenue) p.set("buy_venue", ctx.buyVenue);
  if (ctx.sellVenue) p.set("sell_venue", ctx.sellVenue);
  const qs = p.toString();
  return qs ? `/screener?${qs}` : "/screener";
}

// ResultPanel renders the feasibility outcome first, then the bounded
// estimate, then the shared limitations note (T-087 §C2/§C3) — never the
// reverse, and never a Stat-card grid whose `truncate` is what produced
// the audit's ellipsis-clipped figures.
function ResultPanel({
  result,
  request,
}: {
  result: ScreenerCalculatorResult;
  request: CalculatorRequestContext;
}) {
  const netDisplay = presentSignedQuote(result.net, request.quote);
  const netBpsDisplay = presentBps(result.net_bps);
  const priceUnit = `${request.quote}/${request.base}`;

  return (
    <>
      <Section title="Feasibility">
        {result.liquidity_ok ? (
          <div
            role="status"
            className="flex items-start gap-2 rounded border border-[var(--border-strong)] bg-[var(--bg-panel)] p-3"
          >
            <span className="mt-0.5 shrink-0 text-[var(--ok)]">
              <InfoIcon />
            </span>
            <div>
              <p className="text-[13px] font-medium text-[var(--ok)]">
                Liquidity sufficient for this size
              </p>
              <p className="mt-1 text-[13px] text-[var(--text-dim)]">
                Top-of-book size on {request.buyVenue} and {request.sellVenue}{" "}
                covers this trade size at the prices used. This is still a
                simulated estimate, not a tradable guarantee.
              </p>
            </div>
          </div>
        ) : result.liquidity_unknown ? (
          // A third state, and it has to come before the "insufficient"
          // one. Several venues (Gate, Crypto.com, WhiteBIT) publish
          // bulk tickers with no sizes at all, and
          // internal/screener/calculator.go then computes
          // liquidity_quote as zero — which makes liquidity_ok false for
          // *every* size, including the smallest. Rendering that as
          // "does not cover this trade size … try a smaller size"
          // asserted a depth measurement that was never made and gave
          // advice that can never succeed: shrinking 1000 to 0.01 clears
          // nothing. The backend already distinguishes the two cases;
          // the console simply was not reading the flag.
          <div
            role="status"
            className="flex items-start gap-2 rounded border border-[var(--warn)] bg-[var(--bg-panel)] p-3"
          >
            <span className="mt-0.5 shrink-0 text-[var(--warn)]">
              <WarnTriIcon />
            </span>
            <div>
              <p className="text-[13px] font-medium text-[var(--warn)]">
                Liquidity could not be checked
              </p>
              <p className="mt-1 text-[13px] text-[var(--text-dim)]">
                {request.buyVenue} or {request.sellVenue} publishes no
                top-of-book size, so depth was not measured at this size or at
                any other. The estimate below uses the quoted prices only —
                whether the size is available is unknown, not insufficient.
              </p>
            </div>
          </div>
        ) : (
          <div
            role="status"
            className="flex items-start gap-2 rounded border border-[var(--warn)] bg-[var(--bg-panel)] p-3"
          >
            <span className="mt-0.5 shrink-0 text-[var(--warn)]">
              <WarnTriIcon />
            </span>
            <div>
              <p className="text-[13px] font-medium text-[var(--warn)]">
                Liquidity insufficient for this size
              </p>
              <p className="mt-1 text-[13px] text-[var(--text-dim)]">
                The top-of-book size on {request.buyVenue} and{" "}
                {request.sellVenue} does not cover this trade size at the
                prices used. The estimate below is not an executable
                opportunity at this size — try a smaller size.
              </p>
            </div>
          </div>
        )}
      </Section>

      <Section title="Estimate">
        <dl className="grid max-w-xl grid-cols-[minmax(0,auto)_1fr] gap-x-6 gap-y-2 text-[13px]">
          <dt className="text-[var(--text-dim)]">Buy ask</dt>
          <dd className="min-w-0 break-words text-right">
            <DecimalValue d={presentPrice(result.buy_ask)} />{" "}
            <span className="text-[var(--text-dim)]">{priceUnit}</span>
          </dd>
          <dt className="text-[var(--text-dim)]">Sell bid</dt>
          <dd className="min-w-0 break-words text-right">
            <DecimalValue d={presentPrice(result.sell_bid)} />{" "}
            <span className="text-[var(--text-dim)]">{priceUnit}</span>
          </dd>
          <dt className="text-[var(--text-dim)]">Size (base)</dt>
          <dd className="min-w-0 break-words text-right">
            <DecimalValue d={presentQty(result.size_base, request.base)} />
          </dd>
          <dt className="text-[var(--text-dim)]">Gross</dt>
          <dd className="min-w-0 break-words text-right">
            <DecimalValue d={presentQuote(result.gross, request.quote)} />
          </dd>
          <dt className="text-[var(--text-dim)]">Buy fees</dt>
          <dd className="min-w-0 break-words text-right">
            <DecimalValue d={presentQuote(result.fees_buy, request.quote)} />
          </dd>
          <dt className="text-[var(--text-dim)]">Sell fees</dt>
          <dd className="min-w-0 break-words text-right">
            <DecimalValue d={presentQuote(result.fees_sell, request.quote)} />
          </dd>
          <dt className="text-[var(--text-dim)]">Transfer fee</dt>
          <dd className="min-w-0 break-words text-right">
            <DecimalValue
              d={presentQuote(result.transfer_fee, request.quote)}
            />
          </dd>
          <dt className="font-semibold text-[var(--text-dim)]">Net</dt>
          <dd className="min-w-0 break-words text-right font-semibold">
            <DecimalValue
              d={netDisplay}
              tone={feasibilityTone(netDisplay, result.liquidity_ok)}
            />
          </dd>
          <dt className="font-semibold text-[var(--text-dim)]">Net bps</dt>
          <dd className="min-w-0 break-words text-right font-semibold">
            <DecimalValue
              d={netBpsDisplay}
              tone={feasibilityTone(netBpsDisplay, result.liquidity_ok)}
            />
          </dd>
        </dl>
      </Section>

      <p className="max-w-xl text-[12px] text-[var(--text-dim)]">
        {NO_TRANSFER_NOTE}
      </p>
    </>
  );
}

function CalculatorPageInner() {
  const params = useSearchParams();

  const [base, setBase] = useState(params.get("base") ?? "");
  const [quote, setQuote] = useState(params.get("quote") ?? "USDT");
  // The hand-off's venues are taken verbatim. VENUE_OPTIONS is only six
  // names ("just the chip vocabulary", per its own comment) while the
  // screener covers every venue GET /screener/status reports — fifteen
  // in this deployment. Seeding these from VENUE_OPTIONS[0]/[1] was
  // therefore not merely a narrow list: a <select> whose value is not
  // among its options renders the *first* option, so arriving from a
  // `binance → kucoin` row silently showed `binance → binance` and
  // Calculate would have priced a different trade than the row that was
  // clicked. Substituting a venue is worse than admitting an unknown one.
  const [buyVenue, setBuyVenue] = useState(
    params.get("buy_venue") ?? VENUE_OPTIONS[0]!,
  );
  const [sellVenue, setSellVenue] = useState(
    params.get("sell_venue") ?? VENUE_OPTIONS[1]!,
  );
  const [sizeQuote, setSizeQuote] = useState(params.get("size") ?? "1000");
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [transferFee, setTransferFee] = useState("");
  const [overrideBuyFee, setOverrideBuyFee] = useState("");
  const [overrideSellFee, setOverrideSellFee] = useState("");
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});

  const [state, setState] = useState<ResultState>({ kind: "idle" });

  // venueOptions: every venue the backend reports, plus the two
  // currently selected, so a selected venue is always among the options
  // and can never be silently replaced by the first one. Falls back to
  // the static list before the status response arrives, and a venue the
  // backend does not know is still offered — the backend refuses it and
  // its own error message is shown verbatim, which is honest, whereas
  // quietly pricing a different venue is not.
  const status = useScreenerStatus();
  const venueOptions = useMemo(() => {
    const fromStatus =
      status.kind === "ready"
        ? (status.data.venues ?? []).map((v) => v.name)
        : [];
    const merged = [...new Set([...fromStatus, ...VENUE_OPTIONS])].sort();
    for (const v of [buyVenue, sellVenue]) {
      if (v && !merged.includes(v)) merged.push(v);
    }
    return merged;
  }, [status, buyVenue, sellVenue]);

  const submit = async () => {
    const errs = validate({
      base,
      quote,
      sizeQuote,
      transferFee,
      overrideBuyFee,
      overrideSellFee,
    });
    setFieldErrors(errs);
    if (Object.keys(errs).length > 0) return;

    const request: CalculatorRequestContext = {
      base: base.trim().toUpperCase(),
      quote: quote.trim().toUpperCase(),
      buyVenue,
      sellVenue,
    };
    setState({ kind: "loading" });
    try {
      const result = await api.screener.calculator({
        base: request.base,
        quote: request.quote,
        buy_venue: request.buyVenue,
        sell_venue: request.sellVenue,
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
      setState({ kind: "ready", result, request });
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

  const sizeUnit = quote.trim() ? quote.trim().toUpperCase() : "quote";

  return (
    <ConsoleShell>
      <div className="mb-4 flex items-center justify-between gap-3">
        <PageTitle>Spreads calculator</PageTitle>
        <Link
          href={backToScreenerHref({ base, quote, buyVenue, sellVenue })}
          className="text-[13px] text-[var(--accent)] hover:underline"
        >
          ← Back to Screener
        </Link>
      </div>

      <Section title="Inputs">
        <div className="max-w-2xl space-y-4 text-[13px]">
          <div className="grid grid-cols-2 gap-3">
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Base asset
              </span>
              <input
                value={base}
                onChange={(e) => {
                  setBase(e.target.value);
                  setFieldErrors((p) => ({ ...p, base: undefined }));
                }}
                placeholder="BTC"
                aria-invalid={fieldErrors.base ? true : undefined}
                className={`rounded border bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)] ${fieldErrors.base ? "border-[var(--critical)]" : "border-[var(--border)]"}`}
              />
              <span className="text-[11px] text-[var(--text-dim)]">
                The asset being bought and sold, e.g. BTC.
              </span>
              {fieldErrors.base && (
                <span className="text-[11px] text-[var(--critical)]">
                  {fieldErrors.base}
                </span>
              )}
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Quote asset
              </span>
              <input
                value={quote}
                onChange={(e) => {
                  setQuote(e.target.value);
                  setFieldErrors((p) => ({ ...p, quote: undefined }));
                }}
                placeholder="USDT"
                aria-invalid={fieldErrors.quote ? true : undefined}
                className={`rounded border bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)] ${fieldErrors.quote ? "border-[var(--critical)]" : "border-[var(--border)]"}`}
              />
              <span className="text-[11px] text-[var(--text-dim)]">
                Settlement currency on both venues, e.g. USDT.
              </span>
              {fieldErrors.quote && (
                <span className="text-[11px] text-[var(--critical)]">
                  {fieldErrors.quote}
                </span>
              )}
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
                {venueOptions.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
              <span className="text-[11px] text-[var(--text-dim)]">
                Venue to buy on (takes the ask).
              </span>
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
                {venueOptions.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
              <span className="text-[11px] text-[var(--text-dim)]">
                Venue to sell on (hits the bid).
              </span>
            </label>
          </div>

          <label className="flex max-w-xs flex-col gap-1">
            <span className="text-[12px] text-[var(--text-dim)]">
              Size ({sizeUnit})
            </span>
            <input
              value={sizeQuote}
              onChange={(e) => {
                setSizeQuote(e.target.value);
                setFieldErrors((p) => ({ ...p, size: undefined }));
              }}
              inputMode="decimal"
              aria-invalid={fieldErrors.size ? true : undefined}
              className={`rounded border bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)] ${fieldErrors.size ? "border-[var(--critical)]" : "border-[var(--border)]"}`}
            />
            <span className="text-[11px] text-[var(--text-dim)]">
              Trade size, denominated in the quote asset above.
            </span>
            {fieldErrors.size && (
              <span className="text-[11px] text-[var(--critical)]">
                {fieldErrors.size}
              </span>
            )}
          </label>

          <div className="border-t border-[var(--border)] pt-3">
            <button
              type="button"
              onClick={() => setShowAdvanced((v) => !v)}
              aria-expanded={showAdvanced}
              className="flex items-center gap-1.5 text-[12px] font-medium text-[var(--accent)] hover:underline"
            >
              <PlusMinusIcon open={showAdvanced} />
              {showAdvanced ? "Hide advanced inputs" : "Show advanced inputs"}
            </button>
            {showAdvanced && (
              <div className="mt-3 grid grid-cols-2 gap-3">
                <label className="flex flex-col gap-1">
                  <span className="text-[12px] text-[var(--text-dim)]">
                    Transfer fee (quote, optional)
                  </span>
                  <input
                    value={transferFee}
                    onChange={(e) => {
                      setTransferFee(e.target.value);
                      setFieldErrors((p) => ({ ...p, transferFee: undefined }));
                    }}
                    inputMode="decimal"
                    placeholder="0 (no-transfer model)"
                    aria-invalid={fieldErrors.transferFee ? true : undefined}
                    className={`rounded border bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)] ${fieldErrors.transferFee ? "border-[var(--critical)]" : "border-[var(--border)]"}`}
                  />
                  <span className="text-[11px] text-[var(--text-dim)]">
                    Only applied if you set it — the default model assumes no
                    transfer between venues.
                  </span>
                  {fieldErrors.transferFee && (
                    <span className="text-[11px] text-[var(--critical)]">
                      {fieldErrors.transferFee}
                    </span>
                  )}
                </label>
                <label className="flex flex-col gap-1">
                  <span className="text-[12px] text-[var(--text-dim)]">
                    Override buy fee (bps, optional)
                  </span>
                  <input
                    value={overrideBuyFee}
                    onChange={(e) => {
                      setOverrideBuyFee(e.target.value);
                      setFieldErrors((p) => ({
                        ...p,
                        overrideBuyFee: undefined,
                      }));
                    }}
                    inputMode="decimal"
                    placeholder="venue default"
                    aria-invalid={fieldErrors.overrideBuyFee ? true : undefined}
                    className={`rounded border bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)] ${fieldErrors.overrideBuyFee ? "border-[var(--critical)]" : "border-[var(--border)]"}`}
                  />
                  <span className="text-[11px] text-[var(--text-dim)]">
                    Leave blank to use the venue&apos;s configured taker fee.
                  </span>
                  {fieldErrors.overrideBuyFee && (
                    <span className="text-[11px] text-[var(--critical)]">
                      {fieldErrors.overrideBuyFee}
                    </span>
                  )}
                </label>
                <label className="flex flex-col gap-1">
                  <span className="text-[12px] text-[var(--text-dim)]">
                    Override sell fee (bps, optional)
                  </span>
                  <input
                    value={overrideSellFee}
                    onChange={(e) => {
                      setOverrideSellFee(e.target.value);
                      setFieldErrors((p) => ({
                        ...p,
                        overrideSellFee: undefined,
                      }));
                    }}
                    inputMode="decimal"
                    placeholder="venue default"
                    aria-invalid={
                      fieldErrors.overrideSellFee ? true : undefined
                    }
                    className={`rounded border bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)] ${fieldErrors.overrideSellFee ? "border-[var(--critical)]" : "border-[var(--border)]"}`}
                  />
                  <span className="text-[11px] text-[var(--text-dim)]">
                    Leave blank to use the venue&apos;s configured taker fee.
                  </span>
                  {fieldErrors.overrideSellFee && (
                    <span className="text-[11px] text-[var(--critical)]">
                      {fieldErrors.overrideSellFee}
                    </span>
                  )}
                </label>
              </div>
            )}
          </div>

          <div>
            <Button onClick={submit} disabled={state.kind === "loading"}>
              {state.kind === "loading" ? "Calculating…" : "Calculate"}
            </Button>
            <p className="mt-1 text-[11px] text-[var(--text-dim)]">
              Calculates a read-only estimate — no order is placed on any
              venue.
            </p>
            {state.kind === "error" && (
              <p className="mt-2 text-[13px] text-[var(--critical)]">
                {state.message}
              </p>
            )}
          </div>
        </div>
      </Section>

      {state.kind === "ready" && (
        <ResultPanel result={state.result} request={state.request} />
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
