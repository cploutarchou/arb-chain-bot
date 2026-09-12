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
  return isNegativeDecimalStr(value) ? "bad" : "ok";
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
