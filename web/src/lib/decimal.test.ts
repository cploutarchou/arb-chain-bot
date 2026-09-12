import assert from "node:assert/strict";
import { test } from "node:test";

import { fmtDecimal } from "./decimal.ts";

// The client-area audit's decimal rules (2026-09-12 §2/§4C): bounded
// display precision, no false zero, no minus-zero artifact, exact
// values preserved by the caller for sorting/forms/API. Every case
// here is digit-string behaviour only — no float is involved.

test("long fractional tails are bounded half-up", () => {
  assert.equal(fmtDecimal("20.949999999999999999", { maxFrac: 2 }), "20.95");
  assert.equal(fmtDecimal("1.2345678"), "1.234568");
  assert.equal(fmtDecimal("-0.12345649", { maxFrac: 6 }), "-0.123456");
});

test("rounding carries into the integer part", () => {
  assert.equal(fmtDecimal("9.9999999", { maxFrac: 6 }), "10");
  assert.equal(fmtDecimal("0.9999999999", { maxFrac: 2 }), "1");
});

test("trailing zeros are trimmed, integers stay integers", () => {
  assert.equal(fmtDecimal("49,988".replace(",", ""), { maxFrac: 6 }), "49,988");
  assert.equal(fmtDecimal("49988.0004"), "49,988.0004");
  assert.equal(fmtDecimal("12"), "12");
});

test("thousands separators group the integer part", () => {
  assert.equal(fmtDecimal("1234567.891"), "1,234,567.891");
  assert.equal(fmtDecimal("-9876543.21"), "-9,876,543.21");
  assert.equal(fmtDecimal("1234567.891", { group: false }), "1234567.891");
});

test("a tiny nonzero value never renders as a false zero", () => {
  assert.equal(fmtDecimal("0.0000004"), "< 0.000001");
  assert.equal(fmtDecimal("-0.0000004"), "-< 0.000001");
  assert.equal(fmtDecimal("0.0000004", { maxFrac: 2 }), "< 0.01");
  // Exactly at the rounding boundary rounds up into display range.
  assert.equal(fmtDecimal("0.0000005"), "0.000001");
});

test("zero forms never produce a minus-zero artifact", () => {
  assert.equal(fmtDecimal("0"), "0");
  assert.equal(fmtDecimal("0.0"), "0");
  assert.equal(fmtDecimal("-0"), "0");
  assert.equal(fmtDecimal("-0.000"), "0");
});

test("maxFrac 0 rounds to a whole number", () => {
  assert.equal(fmtDecimal("12.6", { maxFrac: 0 }), "13");
  assert.equal(fmtDecimal("12.4", { maxFrac: 0 }), "12");
});

test("missing values are the em dash, non-numeric passes through", () => {
  assert.equal(fmtDecimal(null), "—");
  assert.equal(fmtDecimal(undefined), "—");
  assert.equal(fmtDecimal(""), "—");
  assert.equal(fmtDecimal("unknown"), "unknown");
});

test("very large exact values keep their digits", () => {
  assert.equal(
    fmtDecimal("1234567890123.4567891", { maxFrac: 4 }),
    "1,234,567,890,123.4568",
  );
});

test("crypto-scale small prices render usefully", () => {
  assert.equal(fmtDecimal("0.00001234", { maxFrac: 8 }), "0.00001234");
  assert.equal(fmtDecimal("0.000012345678", { maxFrac: 8 }), "0.00001235");
});
