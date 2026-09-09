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
  if (t === "—" || t.startsWith("-") || t.startsWith("+")) return value;
  if (/^0(\.0+)?$/.test(t)) return value;
  return `+${value}`;
}
