import { expect, test } from "@playwright/test";
import {
  cmpDecimalStr,
  isZeroDecimalStr,
  presentBps,
  presentDecimal,
  presentFeeBps,
  presentPercentFromFraction,
  presentPrice,
  presentQuote,
  presentQty,
  presentSignedQuote,
  signTone,
} from "@/lib/decimal";

// T-087 client-area refinement. These tests guard the presentation layer
// the audit's overflowing decimal cells required
// (docs/design/client-area-audit-2026-09-12 §2/§3/§4), and they are
// deliberately written against *display* behaviour only: every case also
// asserts that the exact backend string survives untouched, because that
// is the string sorting, filters, forms, exports and API requests use.

// The real values captured in the audit screenshots, so a regression that
// reintroduces twenty-digit cells fails here rather than in review.
// text() is presentDecimal's display string. These cases test *this*
// layer's contract (minFrac padding, exponent normalisation, unit
// labels); master's own `fmtDecimal` has a narrower contract and its own
// suite in src/lib/decimal.test.ts, run by `npm test`.
const text = (raw: string | number | null | undefined, opts = {}) =>
  presentDecimal(raw, opts).text;

const AUDIT_GROSS = "1078.65168539325842696629213";
const AUDIT_NET = "1063.1123595505617977528089";
const AUDIT_LIQUIDITY = "656.0384524";
const AUDIT_SIZE_BASE = "56179.77528089887640449438202";

test.describe("presentDecimal — bounded precision", () => {
  test("bounds the audit's twenty-digit bps values and keeps the exact string", () => {
    const gross = presentBps(AUDIT_GROSS);
    expect(gross.text).toBe("+1,078.65 bps");
    expect(gross.exact).toBe(AUDIT_GROSS);
    expect(gross.rounded).toBe(true);
    expect(gross.srText).toContain(AUDIT_GROSS);

    const net = presentBps(AUDIT_NET);
    expect(net.text).toBe("+1,063.11 bps");
    expect(net.exact).toBe(AUDIT_NET);
  });

  test("rounds on the digit string, not through a float", () => {
    // Number("1078.65168539325842696629213").toFixed(2) has already lost
    // digits before rounding. A 30-digit integer proves the BigInt path:
    // a float would render 1.2345678901234568e+29.
    const huge = presentDecimal("123456789012345678901234567890.987654", {
      maxFrac: 2,
    });
    expect(huge.text).toBe("123,456,789,012,345,678,901,234,567,890.99");
    expect(huge.exact).toBe("123456789012345678901234567890.987654");
    expect(huge.rounded).toBe(true);
  });

  test("propagates a rounding carry", () => {
    expect(text("9.999", { maxFrac: 2 })).toBe("10");
    expect(text("9.999", { maxFrac: 2, minFrac: 2 })).toBe("10.00");
    expect(text("0.999", { maxFrac: 2, minFrac: 2 })).toBe("1.00");
    expect(text("999999.999", { maxFrac: 2 })).toBe("1,000,000");
  });

  test("does not flag an exact value as rounded when the dropped tail is zeros", () => {
    const d = presentDecimal("12.3400000", { maxFrac: 2, minFrac: 2 });
    expect(d.text).toBe("12.34");
    expect(d.rounded).toBe(false);
    expect(d.srText).not.toContain("rounded");
  });

  test("groups thousands and can be asked not to", () => {
    expect(text("435990", { maxFrac: 0 })).toBe("435,990");
    expect(text("435990", { maxFrac: 0, group: false })).toBe("435990");
  });
});

test.describe("presentDecimal — tiny signed values", () => {
  test("a tiny positive value never reads as zero", () => {
    const d = presentDecimal("0.0000000012", { maxFrac: 2, signed: true });
    expect(d.text).toBe("+< 0.01");
    expect(d.tiny).toBe(true);
    expect(d.zero).toBe(false);
    expect(d.exact).toBe("0.0000000012");
  });

  test("a tiny negative value keeps its sign and never reads as zero", () => {
    const d = presentSignedQuote("-0.0000000012", "USDC");
    expect(d.text).toBe("-< 0.01 USDC");
    expect(d.tiny).toBe(true);
    expect(d.zero).toBe(false);
    expect(d.negative).toBe(true);
    // The loss is real; the display must not report nothing.
    expect(d.text).not.toContain("0.00 USDC");
    expect(d.srText).toBe(
      "negative, less than 0.01 USDC, exactly -0.0000000012",
    );
  });

  test("never renders a minus-zero artifact", () => {
    for (const v of ["-0", "-0.0", "-0.000", "-0E-9"]) {
      const d = presentSignedQuote(v, "USDT");
      expect(d.text, `input ${v}`).toBe("0.00 USDT");
      expect(d.text, `input ${v}`).not.toContain("-");
      expect(d.negative, `input ${v}`).toBe(false);
      expect(d.zero, `input ${v}`).toBe(true);
    }
  });

  test("exact zero is zero, not a less-than form, and carries no sign", () => {
    const d = presentSignedQuote("0", "USDC");
    expect(d.text).toBe("0.00 USDC");
    expect(d.zero).toBe(true);
    expect(d.tiny).toBe(false);
    expect(d.rounded).toBe(false);
    expect(text("0", { maxFrac: 2 })).toBe("0");
  });

  test("the tiny threshold follows the requested precision", () => {
    expect(text("0.004", { maxFrac: 2 })).toBe("< 0.01");
    expect(text("0.004", { maxFrac: 4 })).toBe("0.004");
    expect(text("0.4", { maxFrac: 0 })).toBe("< 1");
  });
});

test.describe("presentDecimal — unknown input renders unknown, never zero", () => {
  test("null, undefined, empty and malformed values are dashes", () => {
    for (const v of [null, undefined, "", "   ", "-", ".", "abc", "1.2.3", "NaN", "Infinity"]) {
      const d = presentQuote(v as string | null | undefined, "USDC");
      expect(d.text, `input ${JSON.stringify(v)}`).toBe("—");
      expect(d.valid, `input ${JSON.stringify(v)}`).toBe(false);
      expect(d.exact, `input ${JSON.stringify(v)}`).toBeNull();
      expect(d.zero, `input ${JSON.stringify(v)}`).toBe(false);
      expect(d.srText, `input ${JSON.stringify(v)}`).toBe("unknown");
    }
  });

  test("an unknown value is distinguishable from a zero value", () => {
    expect(presentQuote(null, "USDC").text).not.toBe(
      presentQuote("0", "USDC").text,
    );
  });
});

test.describe("presentDecimal — units and assets", () => {
  test("the asset label is rendered and spoken", () => {
    const usdc = presentQuote("10000", "USDC");
    const usdt = presentQuote("10000", "USDT");
    expect(usdc.text).toBe("10,000.00 USDC");
    expect(usdt.text).toBe("10,000.00 USDT");
    expect(usdc.srText).toContain("USDC");
    // Same figure, different assets: the strings must differ so the two
    // can never be read as one number.
    expect(usdc.text).not.toBe(usdt.text);
  });

  test("no helper sums or merges two assets", () => {
    // There is deliberately no API for combining assets — presentQuote
    // takes exactly one asset label and one value.
    const a = presentQuote("10000", "USDC");
    const b = presentQuote("10000", "USDT");
    expect(`${a.text} · ${b.text}`).toBe("10,000.00 USDC · 10,000.00 USDT");
  });

  test("bps figures are labelled bps and always signed", () => {
    expect(presentBps("13.2919786096256686449197861").text).toBe("+13.29 bps");
    expect(presentBps("-13.29").text).toBe("-13.29 bps");
  });
});

test.describe("presentPrice / presentQty — significant digits below 1", () => {
  test("a small price keeps its meaning instead of collapsing", () => {
    expect(presentPrice("0.00224").text).toBe("0.00224");
    expect(presentPrice("0.0178").text).toBe("0.0178");
    expect(presentPrice("0.01972").text).toBe("0.01972");
    expect(presentPrice("0.00042").text).toBe("0.00042");
  });

  test("a large price is bounded and grouped", () => {
    expect(presentPrice("2524.5").text).toBe("2,524.5");
    expect(presentPrice("2535.9912345678").text).toBe("2,535.9912");
  });

  test("a base quantity is bounded and keeps the exact string", () => {
    const q = presentQty(AUDIT_SIZE_BASE, "AI");
    expect(q.text).toBe("56,179.7753 AI");
    expect(q.rounded).toBe(true);
    expect(q.exact).toBe(AUDIT_SIZE_BASE);
  });

  test("liquidity is bounded to two decimals in quote units", () => {
    const l = presentQuote(AUDIT_LIQUIDITY, "USDT");
    expect(l.text).toBe("656.04 USDT");
    expect(l.exact).toBe(AUDIT_LIQUIDITY);
  });
});

test.describe("presentation never becomes the value", () => {
  test("sorting still uses the exact strings and is unaffected by display", () => {
    // Two values that differ only past float precision: the display
    // rounds them to the same text, the comparison must still order them.
    const a = "1063.1123595505617977528089";
    const b = "1063.1123595505617977528088";
    expect(presentBps(a).text).toBe(presentBps(b).text);
    expect(cmpDecimalStr(a, b)).toBe(1);
    expect(cmpDecimalStr(b, a)).toBe(-1);

    // Sorting a table by the displayed text would be wrong; sorting by
    // the exact string is right.
    const rows = [b, a];
    const sorted = [...rows].sort((x, y) => cmpDecimalStr(y, x));
    expect(sorted).toEqual([a, b]);
  });

  test("the exact value is always reachable for disclosure and submission", () => {
    const d = presentBps(AUDIT_GROSS);
    // What a form, an export or an API request must use:
    expect(d.exact).toBe(AUDIT_GROSS);
    // What the eye reads:
    expect(d.text).toBe("+1,078.65 bps");
    // And the disclosure states the exact figure.
    expect(d.srText).toContain(AUDIT_GROSS);
  });

  test("presenting a value does not mutate or re-encode it", () => {
    const raw = "0.010000";
    const d = presentQuote(raw, "USDC");
    expect(d.exact).toBe(raw); // trailing zeros preserved verbatim
    expect(d.text).toBe("0.01 USDC");
  });

  test("exponent-form input is normalised rather than rejected", () => {
    expect(text("1.5e3", { maxFrac: 2 })).toBe("1,500");
    expect(text("1.5E-4", { maxFrac: 6 })).toBe("0.00015");
    expect(presentDecimal("1.5e3", { maxFrac: 2 }).exact).toBe("1.5e3");
  });
});

test.describe("signTone — a flat result is not a winning one", () => {
  test("exactly zero is neutral, not positive", () => {
    // This returned "ok" before T-087, so an exactly-zero session
    // rendered in the positive colour and read as a gain at a glance.
    for (const v of ["0", "0.0", "0.00", "-0", "-0.000", "+0", "0e-9", "-0E-9"]) {
      expect(signTone(v), `input ${v}`).toBe("dim");
    }
  });

  test("real gains and losses still get their colour", () => {
    expect(signTone("0.01")).toBe("ok");
    expect(signTone("1063.1123595505617977528089")).toBe("ok");
    expect(signTone("-0.01")).toBe("bad");
    expect(signTone("-1063.11")).toBe("bad");
  });

  test("a tiny nonzero loss is still a loss", () => {
    // The display abbreviates it to "-<0.01"; the tone must not round
    // it to neutral along the way.
    expect(signTone("-0.0000000012")).toBe("bad");
    expect(signTone("0.0000000012")).toBe("ok");
  });

  test("unknown values are neutral, never positive", () => {
    expect(signTone(undefined)).toBe("dim");
    expect(signTone(null)).toBe("dim");
    expect(signTone("")).toBe("dim");
    expect(signTone("abc")).toBe("ok"); // non-numeric, non-negative: unchanged legacy behaviour
  });

  test("isZeroDecimalStr recognises zero in any spelling and nothing else", () => {
    for (const v of ["0", "0.0", "-0.00", "+0", "0e5"]) {
      expect(isZeroDecimalStr(v), `input ${v}`).toBe(true);
    }
    for (const v of ["0.01", "-0.0001", "1", "", null, undefined, "abc", "."]) {
      expect(isZeroDecimalStr(v as string | null | undefined), `input ${v}`).toBe(false);
    }
  });
});

test.describe("presentPercentFromFraction — a ratio is not an amount", () => {
  test("converts a fraction to a percentage exactly", () => {
    // internal/portfolio/portfolio.go computes drawdown as
    // peak.Sub(equity).Div(peak) — a dimensionless fraction, emitted at
    // StringFixed(4). It was being rendered through presentSignedQuote
    // with the start asset as its unit, so a 5.23% drawdown displayed as
    // "+0.05 USDC": a fabricated currency unit and, on a 10,000 USDC
    // peak, a ~200x understatement of a risk figure.
    expect(presentPercentFromFraction("0.0523").text).toBe("5.23%");
    expect(presentPercentFromFraction("0.0040").text).toBe("0.40%");
    expect(presentPercentFromFraction("0.1234").text).toBe("12.34%");
    expect(presentPercentFromFraction("1.0000").text).toBe("100.00%");
  });

  test("small drawdowns stay distinguishable instead of collapsing", () => {
    // Reading the 4-dp field at 2 dp put every drawdown below 0.5% into
    // the same "< 0.01" bucket, so 0.40% and 0.01% looked identical.
    const a = presentPercentFromFraction("0.0040");
    const b = presentPercentFromFraction("0.0001");
    expect(a.text).not.toBe(b.text);
    expect(a.text).toBe("0.40%");
    expect(b.text).toBe("0.01%");
  });

  test("the conversion is exact, never a float multiply", () => {
    // Number("0.0007") * 100 is 0.06999999999999999.
    expect(presentPercentFromFraction("0.0007").text).toBe("0.07%");
    expect(presentPercentFromFraction("0.0029").text).toBe("0.29%");
  });

  test("zero and unknown are still distinguishable", () => {
    expect(presentPercentFromFraction("0.0000").text).toBe("0.00%");
    expect(presentPercentFromFraction("0.0000").zero).toBe(true);
    for (const v of [null, undefined, ""]) {
      const d = presentPercentFromFraction(v as string | null | undefined);
      expect(d.text).toBe("—");
      expect(d.valid).toBe(false);
    }
  });

  test("the exact backend fraction survives for disclosure", () => {
    // `exact` carries the converted percentage string, which is what the
    // reader is being shown; the original fraction is one shift away and
    // is never lost to a float.
    expect(presentPercentFromFraction("0.0523").exact).toBe("5.23");
  });

  test("the percent sign sits against the number, units keep their space", () => {
    expect(presentPercentFromFraction("0.0523").text).toBe("5.23%");
    expect(presentQuote("1", "USDC").text).toBe("1.00 USDC");
    expect(presentBps("1").text).toBe("+1.00 bps");
    // Spoken form always keeps the space so it reads as words.
    expect(presentPercentFromFraction("0.0523").srText).toContain(" %");
  });
});

test.describe("costs and fee rates are unsigned", () => {
  test("a fee rate does not render as a credit", () => {
    // presentBps sets signed, which rendered a 10 bps taker fee as
    // "+10.00 bps".
    expect(presentFeeBps("10").text).toBe("10.00 bps");
    expect(presentBps("10").text).toBe("+10.00 bps");
  });

  test("a cost amount does not render as a credit", () => {
    // fees_marked / fees / daily_loss are all >= 0 from the backend.
    expect(presentQuote("45", "USDC").text).toBe("45.00 USDC");
    expect(presentSignedQuote("45", "USDC").text).toBe("+45.00 USDC");
  });

  test("a real result still carries its sign", () => {
    expect(presentSignedQuote("-45", "USDC").text).toBe("-45.00 USDC");
    expect(presentSignedQuote("45", "USDC").text).toBe("+45.00 USDC");
  });
});
