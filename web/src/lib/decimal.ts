// Exact decimal-string helpers for values the backend treats as exact
// decimals (money, bps, APR fractions) but the console only ever needs
// to reformat or sign-check for display — never recompute. `Number(x)`
// followed by `/100`, `*100`, or a `>`/`<` comparison introduces binary-
// float noise on values that then get sent back to the API (a typed
// "1.1" persisting as "0.011000000000000001" is the bug this file
// exists to prevent) or silently rounds a comparison the backend would
// have done exactly. Every function here is string/digit manipulation
// only; none of them are a profitability, fee, or risk calculation.
import type { Tone } from "@/components/ui";

// shiftDecimalPoint moves the decimal point of an exact decimal string by
// `places` (positive = right, negative = left) using only digit
// manipulation, so the result is exact for any finite decimal input.
// Non-numeric input (empty string, a value still being typed, "-" alone)
// passes through unchanged — callers only ever run this at a form's
// load/submit boundary, never on every keystroke.
function shiftDecimalPoint(raw: string, places: number): string {
  const trimmed = raw.trim();
  if (trimmed === "" || !/^-?\d*\.?\d*$/.test(trimmed) || trimmed === "-") {
    return raw;
  }
  const neg = trimmed.startsWith("-");
  const unsigned = neg ? trimmed.slice(1) : trimmed;
  const dot = unsigned.indexOf(".");
  const intPart = dot === -1 ? unsigned : unsigned.slice(0, dot);
  const fracPart = dot === -1 ? "" : unsigned.slice(dot + 1);
  let digits = intPart + fracPart;
  let pointPos = intPart.length + places;
  if (pointPos < 0) {
    digits = "0".repeat(-pointPos) + digits;
    pointPos = 0;
  }
  if (pointPos > digits.length) {
    digits = digits + "0".repeat(pointPos - digits.length);
  }
  let newInt = digits.slice(0, pointPos).replace(/^0+(?=\d)/, "");
  const newFrac = digits.slice(pointPos).replace(/0+$/, "");
  if (newInt === "") newInt = "0";
  const result = newFrac ? `${newInt}.${newFrac}` : newInt;
  return neg && result !== "0" ? `-${result}` : result;
}

// fractionToPercentStr: wire fraction ("0.07") -> form percent ("7").
export function fractionToPercentStr(fraction: string): string {
  return shiftDecimalPoint(fraction, 2);
}

// percentToFractionStr: form percent ("7") -> wire fraction ("0.07").
// Use this instead of `Number(percent) / 100` anywhere a percent-labelled
// field feeds a fraction-typed API field (min_carry_apr and similar) —
// the division reliably introduces trailing float noise the exact string
// shift never does.
export function percentToFractionStr(percent: string): string {
  return shiftDecimalPoint(percent, -2);
}

// isNegativeDecimalStr: string-based sign check for a backend decimal —
// never `Number(value) < 0`, which both risks float noise on very large
// or precise values and silently reads "" / non-numeric input as 0
// (not-negative) instead of "unknown".
export function isNegativeDecimalStr(value: string | undefined | null): boolean {
  return !!value && value.trim().startsWith("-");
}

// cmpDecimalStr: exact comparison of two backend decimal strings
// (-1 / 0 / +1). Digit normalisation only — `Number(a) - Number(b)`
// loses precision on the values the backend sends as exact strings and
// mis-sorts rows whose bps differ past float accuracy. Non-numeric input
// compares as 0 so a malformed cell never throws during a render sort.
export function cmpDecimalStr(a: string | undefined | null, b: string | undefined | null): number {
  const norm = (v: string | undefined | null) => {
    const t = (v ?? "").trim();
    if (t === "" || !/^-?\d*\.?\d*$/.test(t)) return { neg: false, int: "0", frac: "" };
    const neg = t.startsWith("-");
    const unsigned = neg ? t.slice(1) : t;
    const [i0, f = ""] = unsigned.split(".");
    const i = i0 ?? "";
    return { neg, int: i.replace(/^0+(?=\d)/, "") || "0", frac: f.replace(/0+$/, "") };
  };
  const A = norm(a), B = norm(b);
  if (A.neg !== B.neg) return A.neg ? -1 : 1;
  const mag = (x: { int: string; frac: string }) => {
    const li = x.int.length, lf = x.frac.length;
    const n = Math.max(li, lf);
    return (x.int.padStart(n, "0") + x.frac.padEnd(n, "0")).replace(/^0+(?=\d)/, "") || "0";
  };
  let c = mag(A).length - mag(B).length;
  if (c === 0) c = mag(A).localeCompare(mag(B));
  return A.neg ? -c : c;
}

// signTone/signedText: the shared sign-to-colour and sign-to-prefix rules
// (design-system.md §1.8 — "sign is on the number, never a bare figure
// that needs the colour to be read"). String-based throughout: reading
// the sign is a character check on the backend's own string, not a
// parse-and-recompute.
export function signTone(value: string | undefined | null): Tone {
  if (!value) return "dim";
  // Zero is neither a gain nor a loss. This previously fell through to
  // "ok", so an exactly-zero result rendered in the positive colour —
  // a flat session read as a winning one at a glance. Any all-zero
  // decimal spelling counts ("0", "0.00", "-0.000", "0e-9").
  if (isZeroDecimalStr(value)) return "dim";
  return isNegativeDecimalStr(value) ? "bad" : "ok";
}

// isZeroDecimalStr: true when the string denotes exactly zero, whatever
// spelling the backend used. Digit inspection only, never a parse.
export function isZeroDecimalStr(value: string | undefined | null): boolean {
  if (!value) return false;
  const t = value.trim();
  if (!/^[+-]?\d*\.?\d*([eE][+-]?\d+)?$/.test(t)) return false;
  const digits = t.replace(/^[+-]/, "").split(/[eE]/)[0] ?? "";
  return digits.replace(".", "") !== "" && /^0*\.?0*$/.test(digits);
}

export function signedText(value: string | undefined | null): string {
  if (value === undefined || value === null || value === "") return "—";
  const t = value.trim();
  if (t === "—" || t.startsWith("-") || t.startsWith("+") || t.startsWith("<")) return value;
  if (/^0(\.0+)?$/.test(t)) return value;
  return `+${value}`;
}

// subtractDecimalStr: exact subtraction of two finite decimal strings
// (BigInt-scaled — digit-exact for any length the backend sends, never
// a binary float). Display-only: the one caller is the orders table's
// "remaining quantity" column (requested − filled), a convenience read
// of two values the backend already sent; anything that feeds a
// decision, a ledger or a persisted row stays server-side in
// shopspring/decimal. null when either input is missing or malformed —
// "unknown", never a fabricated 0.
export function subtractDecimalStr(
  a: string | undefined | null,
  b: string | undefined | null,
): string | null {
  const parse = (v: string | undefined | null): { neg: boolean; digits: string; scale: number } | null => {
    if (typeof v !== "string") return null;
    const t = v.trim();
    if (!/^-?\d+(\.\d+)?$/.test(t) || t === "-" || t === "") return null;
    const neg = t.startsWith("-");
    const unsigned = neg ? t.slice(1) : t;
    const dot = unsigned.indexOf(".");
    const intPart = dot === -1 ? unsigned : unsigned.slice(0, dot);
    const fracPart = dot === -1 ? "" : unsigned.slice(dot + 1);
    return {
      neg,
      digits: (intPart.replace(/^0+(?=\d)/, "") + fracPart) || "0",
      scale: fracPart.length,
    };
  };
  const x = parse(a);
  const y = parse(b);
  if (!x || !y) return null;
  const scale = Math.max(x.scale, y.scale);
  const scaled = (p: { neg: boolean; digits: string; scale: number }) => {
    const d = p.scale < scale ? p.digits + "0".repeat(scale - p.scale) : p.digits;
    const v = BigInt(d === "" ? "0" : d);
    return p.neg ? -v : v;
  };
  const diff = scaled(x) - scaled(y);
  const neg = diff < 0n;
  const abs = (neg ? -diff : diff).toString().padStart(scale + 1, "0");
  const intPart = scale === 0 ? abs : abs.slice(0, abs.length - scale);
  const fracPart = scale === 0 ? "" : abs.slice(abs.length - scale).replace(/0+$/, "");
  const body = fracPart ? `${intPart}.${fracPart}` : intPart;
  return body === "0" ? "0" : neg ? `-${body}` : body;
}

// ---- Bounded presentation of exact decimals -------------------------------
// The audit (docs/design/client-area-audit-2026-09-12 §2/§3/§4) found gross
// and net bps rendered with more than twenty fractional digits, a truncated
// drawdown figure on Overview, and Calculator cards clipped with ellipses.
// Every one of those is a *presentation* defect: the backend sends an exact
// decimal string and the console printed it verbatim into a fixed-width cell.
//
// The rules this section implements, and why each one is a rule:
//
//   * Rounding happens on the digit string with BigInt, never through
//     `Number()`/`toFixed()`. `Number("1078.65168539325842696629213")`
//     has already lost digits before `toFixed(2)` ever rounds, so the
//     float route is wrong even for something billed as display-only.
//   * The exact string always survives. `DecimalDisplay.exact` is the
//     untouched backend value, and it is what sorting (`cmpDecimalStr`),
//     filters, forms, exports and API requests keep using. Presentation
//     is a leaf operation; nothing downstream of it is a decision.
//   * A tiny nonzero value never collapses to a true zero. It renders as
//     a signed less-than form (`-< 0.01`), because showing `0.00` for a
//     real loss, or `-0.00` for anything, are both lies about the value.
//   * Exact zero renders as zero and never as `-0`.
//   * Invalid, empty and missing input render as `—` (unknown), never 0.
//   * Units are never implied. Callers pass `unit` so USDC and USDT
//     figures stay labelled and separate; nothing here ever sums assets.
//
// None of this is a financial calculation: no fee, spread, profitability
// or risk figure is derived, only re-rendered at bounded precision.

export interface DecimalDisplay {
  // text: the bounded, grouped, signed presentation form. Safe to put in
  // a fixed-width cell. Never the carrier of exactness on its own.
  text: string;
  // exact: the backend's own string, trimmed. null when the input was
  // missing or malformed. This is the value to disclose on demand and
  // the only value that may be sorted, submitted or exported.
  exact: string | null;
  // rounded: text is not the exact value — a full-precision disclosure
  // is required next to it (title attribute, expander, or detail row).
  rounded: boolean;
  // tiny: nonzero, but smaller in magnitude than the smallest unit the
  // chosen precision can show; text is the signed less-than form.
  tiny: boolean;
  // zero: the input was exactly zero (not merely rounded to zero).
  zero: boolean;
  // negative: sign read off the exact string, never off a parsed number.
  negative: boolean;
  valid: boolean;
  // srText: a spoken form for assistive technology, which cannot read
  // "-<0.01" or a bare grouped figure and know what it means. Includes
  // the unit and says so when the figure is abbreviated.
  srText: string;
}

export interface PresentOptions {
  // maxFrac: hard cap on fraction digits. Ignored when sigFigs applies
  // to a sub-1 magnitude (see resolveFrac).
  maxFrac?: number;
  // minFrac: pad with trailing zeros up to this many fraction digits so
  // a numeric column aligns. Exact zero honours it too ("0.00").
  minFrac?: number;
  // sigFigs: for magnitudes below 1, keep this many significant digits
  // instead of obeying maxFrac — a price of 0.00224 must not present as
  // "<0.01". Capped by hardFracCap so a pathological input cannot
  // produce an unbounded string.
  sigFigs?: number;
  // unit: asset or unit label ("USDC", "bps", "s"). Rendered after the
  // figure and spoken in srText. Never defaulted — an unlabelled money
  // figure is how two different assets end up compared by eye.
  unit?: string;
  // signed: always show an explicit + on positive values (the sign is on
  // the number, never left to colour alone — design-system.md §1.8).
  signed?: boolean;
  // group: thousands separators. On by default; off for identifiers and
  // anything copied as a value.
  group?: boolean;
  // dash: what an unknown value renders as.
  dash?: string;
}

const HARD_FRAC_CAP = 18;
const DEFAULT_MAX_FRAC = 2;

interface ParsedDecimal {
  neg: boolean;
  int: string; // no leading zeros, "0" when empty
  frac: string; // no trailing zeros
}

// parseDecimalStr normalises an exact decimal string, including the
// exponent form, into sign/integer/fraction digit strings. Digit
// manipulation only — the value is never held in a float, so an input of
// any length survives intact. null for anything that is not a finite
// decimal, so the caller renders "unknown" rather than a fabricated 0.
function parseDecimalStr(raw: string): ParsedDecimal | null {
  const t = raw.trim();
  const m = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(t);
  if (!m) return null;
  const [, sign, intRaw = "", fracRaw = "", expRaw] = m;
  if (intRaw === "" && fracRaw === "") return null; // "", "-", ".", "e5"
  const exp = expRaw ? Number(expRaw) : 0;
  if (!Number.isFinite(exp) || Math.abs(exp) > 10000) return null;
  // Shift the point by `exp` on the digit string (exponent notation is
  // not what shopspring/decimal emits, but a value that has been through
  // a JS number somewhere upstream can arrive this way).
  let digits = intRaw + fracRaw;
  let pointPos = intRaw.length + exp;
  if (pointPos < 0) {
    digits = "0".repeat(-pointPos) + digits;
    pointPos = 0;
  }
  if (pointPos > digits.length) {
    digits = digits + "0".repeat(pointPos - digits.length);
  }
  const int = digits.slice(0, pointPos).replace(/^0+(?=\d)/, "") || "0";
  const frac = digits.slice(pointPos).replace(/0+$/, "");
  return { neg: sign === "-", int, frac };
}

function isZeroParsed(p: ParsedDecimal): boolean {
  return /^0*$/.test(p.int) && p.frac === "";
}

// resolveFrac decides how many fraction digits this particular value
// gets. Above 1 in magnitude, maxFrac governs. Below 1, sigFigs (when
// asked for) wins so small prices keep their meaning: 0.00224 at four
// significant digits is "0.002240", not "<0.01". Always bounded by
// HARD_FRAC_CAP.
function resolveFrac(p: ParsedDecimal, opts: PresentOptions): number {
  const maxFrac = Math.min(opts.maxFrac ?? DEFAULT_MAX_FRAC, HARD_FRAC_CAP);
  const sigFigs = opts.sigFigs;
  if (!sigFigs || sigFigs <= 0) return maxFrac;
  if (p.int !== "0") return maxFrac;
  const leadingZeros = p.frac.length - p.frac.replace(/^0+/, "").length;
  return Math.min(leadingZeros + sigFigs, HARD_FRAC_CAP);
}

function group3(int: string): string {
  return int.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

// roundParsed rounds the digit string half-up at `frac` fraction digits
// using BigInt, so carries propagate ("9.999" -> "10.00") and no digit is
// lost before the rounding decision is taken. Returns the rounded
// int/frac pair plus whether anything was actually discarded — a value
// whose dropped tail is all zeros is still exact and must not be flagged
// as abbreviated.
function roundParsed(
  p: ParsedDecimal,
  frac: number,
): { int: string; frac: string; lost: boolean } {
  if (p.frac.length <= frac) {
    return { int: p.int, frac: p.frac.padEnd(frac, "0").slice(0, Math.max(frac, p.frac.length)), lost: false };
  }
  const kept = p.int + p.frac.slice(0, frac);
  const rest = p.frac.slice(frac);
  const lost = /[1-9]/.test(rest);
  const roundUp = rest.charCodeAt(0) >= 53; // '5'
  const bumped = (BigInt(kept === "" ? "0" : kept) + (roundUp ? 1n : 0n)).toString();
  const padded = bumped.padStart(frac + 1, "0");
  const cut = padded.length - frac;
  return {
    int: padded.slice(0, cut).replace(/^0+(?=\d)/, "") || "0",
    frac: frac === 0 ? "" : padded.slice(cut),
    lost,
  };
}

// presentDecimal is the single entry point every money, bps and price
// cell in the console goes through.
export function presentDecimal(
  raw: string | number | null | undefined,
  opts: PresentOptions = {},
): DecimalDisplay {
  const dash = opts.dash ?? "—";
  const unitSuffix = opts.unit ? ` ${opts.unit}` : "";
  const unitSpoken = opts.unit ? ` ${opts.unit}` : "";
  const invalid: DecimalDisplay = {
    text: dash,
    exact: null,
    rounded: false,
    tiny: false,
    zero: false,
    negative: false,
    valid: false,
    srText: "unknown",
  };
  if (raw === null || raw === undefined) return invalid;
  const rawStr = typeof raw === "number" ? String(raw) : raw;
  if (typeof rawStr !== "string") return invalid;
  const parsed = parseDecimalStr(rawStr);
  if (!parsed) return invalid;

  const exact = rawStr.trim();
  const minFrac = Math.min(opts.minFrac ?? 0, HARD_FRAC_CAP);
  const frac = Math.max(resolveFrac(parsed, opts), minFrac);
  const group = opts.group !== false;
  const zero = isZeroParsed(parsed);

  // Exact zero: render zero, honour minFrac for column alignment, and
  // never carry a sign — "-0" and "+0" are both artifacts.
  if (zero) {
    const body = minFrac > 0 ? `0.${"0".repeat(minFrac)}` : "0";
    return {
      text: `${body}${unitSuffix}`,
      exact,
      rounded: false,
      tiny: false,
      zero: true,
      negative: false,
      valid: true,
      srText: `zero${unitSpoken}`,
    };
  }

  const r = roundParsed(parsed, frac);
  const roundedToZero = /^0*$/.test(r.int) && /^0*$/.test(r.frac);

  // Tiny nonzero: the value exists but is smaller than this precision can
  // show. A signed less-than form keeps both the sign and the magnitude
  // bound honest; a "0.00" here would report a real loss as nothing.
  if (roundedToZero) {
    const smallest = frac > 0 ? `0.${"0".repeat(frac - 1)}1` : "1";
    const sign = parsed.neg ? "-" : opts.signed ? "+" : "";
    // "< 0.01" / "-< 0.01" — the spacing master's fmtDecimal already
    // ships and tests. Two less-than forms differing only by a space
    // would be a visible inconsistency between two parts of one table.
    return {
      text: `${sign}< ${smallest}${unitSuffix}`,
      exact,
      rounded: true,
      tiny: true,
      zero: false,
      negative: parsed.neg,
      valid: true,
      srText: `${parsed.neg ? "negative, " : ""}less than ${smallest}${unitSpoken}, exactly ${exact}`,
    };
  }

  const trimmedFrac = r.frac.replace(/0+$/, "");
  const shownFrac =
    trimmedFrac.length >= minFrac
      ? trimmedFrac
      : trimmedFrac.padEnd(minFrac, "0");
  const intText = group ? group3(r.int) : r.int;
  const sign = parsed.neg ? "-" : opts.signed ? "+" : "";
  const body = shownFrac ? `${intText}.${shownFrac}` : intText;
  const text = `${sign}${body}${unitSuffix}`;
  return {
    text,
    exact,
    rounded: r.lost,
    tiny: false,
    zero: false,
    negative: parsed.neg,
    valid: true,
    srText: r.lost
      ? `${sign}${body}${unitSpoken}, rounded; exactly ${exact}`
      : `${sign}${body}${unitSpoken}`,
  };
}

// There is deliberately no `fmtDecimal` here: master's own
// `fmtDecimal` below is the text-only helper, with its own tests and
// its own contract. Where a caller needs just a string, use
// `presentDecimal(raw, opts).text`; where the exact value should stay
// reachable, use <DecimalValue> with one of the presets.

// ---- Presets -------------------------------------------------------------
// Named presets keep precision decisions in one place instead of letting
// each page pick its own maxFrac. Each one states the unit explicitly.

// presentBps: basis points, two decimals, always signed. The audit's
// twenty-digit gross/net cells are this preset's reason to exist.
export function presentBps(raw: string | null | undefined): DecimalDisplay {
  return presentDecimal(raw, { maxFrac: 2, minFrac: 2, unit: "bps", signed: true });
}

// presentQuote: an amount in a quote asset. The asset label is required,
// never defaulted — an unlabelled figure is how USDC and USDT end up read
// as one number.
export function presentQuote(
  raw: string | null | undefined,
  asset: string,
): DecimalDisplay {
  return presentDecimal(raw, { maxFrac: 2, minFrac: 2, unit: asset });
}

// presentSignedQuote: presentQuote for a result figure, where the sign is
// the point (net PnL, drawdown, realized/marked results).
export function presentSignedQuote(
  raw: string | null | undefined,
  asset: string,
): DecimalDisplay {
  return presentDecimal(raw, {
    maxFrac: 2,
    minFrac: 2,
    unit: asset,
    signed: true,
  });
}

// presentPrice: a venue price. Significant-digit driven below 1 so
// 0.00224 keeps its digits instead of collapsing to "<0.01"; four
// decimals above 1. No unit — the pair label carries it.
export function presentPrice(raw: string | null | undefined): DecimalDisplay {
  return presentDecimal(raw, { maxFrac: 4, sigFigs: 4 });
}

// presentQty: a base-asset quantity. Same significant-digit treatment as
// a price; the base asset is the unit when the caller knows it.
export function presentQty(
  raw: string | null | undefined,
  asset?: string,
): DecimalDisplay {
  return presentDecimal(raw, { maxFrac: 4, sigFigs: 4, unit: asset });
}

// fmtDecimal: bounded, grouped display form for an exact backend
// decimal string (client-area audit 2026-09-12: gross/net bps cells
// rendered twenty-plus fractional digits, which made the screener
// table impossible to scan). Digit-string and BigInt manipulation
// only — rounding a DISPLAY, never recomputing the value; the raw
// string stays the source of truth for sorting, filters, forms,
// exports and API round-trips, and callers expose it verbatim as the
// exact-value disclosure (title attribute).
//
// Rules the audit set, all tested below:
//   - bound fractional digits (default 6; bps call sites use 2),
//     half-up on the digit string, trailing zeros trimmed;
//   - a tiny nonzero value that would render as "0" shows as
//     "< 0.000001" (sign retained: "-< 0.000001"), never a false
//     zero;
//   - "-0"/"-0.0" is an artifact and renders "0";
//   - thousands separators on the integer part (opt out with
//     { group: false });
//   - null/undefined/"" → "—"; a non-numeric string passes through
//     unchanged ("unknown" stays "unknown", never throws a render).
export function fmtDecimal(
  raw: string | null | undefined,
  opts: { maxFrac?: number; group?: boolean } = {},
): string {
  if (raw === null || raw === undefined || raw === "") return "—";
  const t = typeof raw === "string" ? raw.trim() : String(raw);
  if (!/^-?\d+(\.\d+)?$/.test(t)) return t;
  const maxFrac = Math.max(0, Math.floor(opts.maxFrac ?? 6));
  const group = opts.group ?? true;

  const neg = t.startsWith("-");
  const unsigned = neg ? t.slice(1) : t;
  const dot = unsigned.indexOf(".");
  const intPart = dot === -1 ? unsigned : unsigned.slice(0, dot);
  const fracPart = dot === -1 ? "" : unsigned.slice(dot + 1);

  // Scale to maxFrac fractional digits, rounding half-up on the digit
  // that would be dropped (BigInt keeps this exact at any length).
  const cut = intPart.length + maxFrac;
  const digits = (intPart + fracPart).padEnd(cut + 1, "0");
  let scaled = BigInt(digits.slice(0, cut) || "0");
  if ((digits[cut] ?? "0") >= "5") scaled += 1n;
  const s = scaled.toString().padStart(maxFrac + 1, "0");
  const intLen = s.length - maxFrac;
  const intOut = s.slice(0, intLen).replace(/^0+(?=\d)/, "") || "0";
  const fracOut = s.slice(intLen).replace(/0+$/, "");

  if (intOut === "0" && fracOut === "") {
    const exactlyZero = /^0*$/.test(intPart + fracPart);
    if (exactlyZero) return "0";
    const floor = maxFrac === 0 ? "1" : `0.${"0".repeat(maxFrac - 1)}1`;
    return neg ? `-< ${floor}` : `< ${floor}`;
  }
  const grouped = group ? intOut.replace(/\B(?=(\d{3})+(?!\d))/g, ",") : intOut;
  const out = fracOut ? `${grouped}.${fracOut}` : grouped;
  return neg ? `-${out}` : out;
}
