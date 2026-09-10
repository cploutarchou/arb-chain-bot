// Field registry for the structured Strategy/Risk/Notifications form
// (BL-14). Mirrors internal/strategy/params.go's Validate() bounds
// exactly; client-side checks here are UX sugar only — the backend's
// response is always authoritative and replaces this copy on a 400
// (console-ux-audit.md §4.4).

import type { StrategyParams } from "@/lib/api/client";

export type ConfigSection = "scanner" | "risk" | "notifications";

export interface FieldSpec {
  section: ConfigSection;
  key: string;
  /** Dotted path matching the JSON field exactly, e.g. "scanner.ttl_ms". */
  path: string;
  label: string;
  kind: "decimal" | "int";
  min?: number;
  max?: number;
  minExclusive?: boolean;
  maxExclusive?: boolean;
  /** Bound-violation copy, plain units (§4.4). */
  help: string;
  effect: "immediate" | "on restart";
  /** Unit suffix rendered INSIDE the field (F11): the operator edits
   * "5 bps", not a bare 5 whose meaning lives three pages away. */
  unit?: string;
  /** Asset an amount field is denominated in ("start asset"). */
  unitAsset?: string;
  /** display selects how the wire value maps to the form's text (F11):
   * "percent" fields are fractions on the wire (0..1) shown and edited
   * as percents through the exact string-shift helpers — never a
   * Number()/100 that invents float noise. */
  display?: "percent";
  /** Default from strategy.DefaultParams() (F11), shown under the field. */
  defaultValue?: string;
  /** One-line consequence of moving this field (F11). */
  consequence?: string;
}

export const SCANNER_FIELDS: FieldSpec[] = [
  {
    section: "scanner",
    key: "latency_buffer_bps",
    path: "scanner.latency_buffer_bps",
    label: "Latency buffer",
    kind: "decimal",
    min: 0,
    max: 1000,
    help: "Must be between 0 and 1,000 bps.",
    effect: "immediate",
      unit: "bps", defaultValue: "5", consequence: "Edge must clear fees + this buffer + the risk buffer before an opportunity qualifies.",
  },
  {
    section: "scanner",
    key: "risk_buffer_bps",
    path: "scanner.risk_buffer_bps",
    label: "Risk buffer",
    kind: "decimal",
    min: 0,
    max: 1000,
    help: "Must be between 0 and 1,000 bps.",
    effect: "immediate",
      unit: "bps", defaultValue: "5", consequence: "Added safety margin on top of the latency buffer; raises the qualification bar.",
  },
  {
    section: "scanner",
    key: "ttl_ms",
    path: "scanner.ttl_ms",
    label: "Opportunity TTL",
    kind: "int",
    min: 50,
    max: 10000,
    help: "Opportunity TTL must be between 50 and 10,000 ms.",
    effect: "immediate",
      unit: "ms", defaultValue: "400", consequence: "How long a qualified opportunity stays executable before expiring untouched.",
  },
  {
    section: "scanner",
    key: "min_input",
    path: "scanner.min_input",
    label: "Minimum input size",
    kind: "decimal",
    min: 0,
    minExclusive: true,
    help: "Must be a positive amount.",
    effect: "immediate",
      unitAsset: "start asset", defaultValue: "50", consequence: "Sizes below this never qualify; sets the smallest cycle the engine will run.",
  },
  {
    section: "scanner",
    key: "depth",
    path: "scanner.depth",
    label: "Book depth",
    kind: "int",
    min: 5,
    max: 500,
    help: "Must be between 5 and 500 book levels.",
    effect: "immediate",
      unit: "levels", defaultValue: "50", consequence: "Book levels the walk sees; deeper is more accurate and slower per evaluation.",
  },
  {
    section: "scanner",
    key: "grid_points",
    path: "scanner.grid_points",
    label: "Size-search grid points",
    kind: "int",
    min: 3,
    max: 41,
    help: "Must be between 3 and 41.",
    effect: "immediate",
      defaultValue: "13", consequence: "Coarse size-search resolution; more points widen the first pass.",
  },
  {
    section: "scanner",
    key: "refine_iters",
    path: "scanner.refine_iters",
    label: "Size-search refine iterations",
    kind: "int",
    min: 0,
    max: 40,
    help: "Must be between 0 and 40.",
    effect: "immediate",
      defaultValue: "14", consequence: "Refinement passes around the best coarse candidate.",
  },
  {
    section: "scanner",
    key: "max_book_age_ms",
    path: "scanner.max_book_age_ms",
    label: "Max book age (scanner)",
    kind: "int",
    min: 100,
    max: 60000,
    help: "Must be between 100 and 60,000 ms.",
    effect: "immediate",
      unit: "ms", defaultValue: "2000", consequence: "A leg older than this fails the scanner-side freshness gate.",
  },
  {
    section: "scanner",
    key: "workers",
    path: "scanner.workers",
    label: "Scanner workers",
    kind: "int",
    min: 1,
    max: 32,
    help: "Must be between 1 and 32.",
    // params.go:40 — applies at component start, not on hot swap.
    effect: "on restart",
      defaultValue: "2", consequence: "Parallel evaluation workers; applies on restart.",
  },
];

export const RISK_FIELDS: FieldSpec[] = [
  {
    section: "risk",
    key: "min_net_edge_bps",
    path: "risk.min_net_edge_bps",
    label: "Min net edge",
    kind: "decimal",
    min: 0,
    help: "Must be 0 or greater.",
    effect: "immediate",
      unit: "bps", defaultValue: "5", consequence: "Opportunities below this net edge are rejected by the deterministic gate.",
  },
  {
    section: "risk",
    key: "min_expected_profit",
    path: "risk.min_expected_profit",
    label: "Min expected profit",
    kind: "decimal",
    min: 0,
    help: "Must be 0 or greater.",
    effect: "immediate",
      unitAsset: "start asset", defaultValue: "1", consequence: "Absolute profit floor per cycle, independent of percentage edge.",
  },
  {
    section: "risk",
    key: "max_trade_size",
    path: "risk.max_trade_size",
    label: "Max trade size",
    kind: "decimal",
    min: 0,
    minExclusive: true,
    help: "Must be a positive amount.",
    effect: "immediate",
      unitAsset: "start asset", defaultValue: "1000", consequence: "Hard cap on a single cycle's input.",
  },
  {
    section: "risk",
    key: "max_capital_per_triangle",
    path: "risk.max_capital_per_triangle",
    label: "Max capital per triangle",
    kind: "decimal",
    min: 0,
    minExclusive: true,
    help: "Must be a positive amount.",
    effect: "immediate",
      unitAsset: "start asset", defaultValue: "2000", consequence: "Cap on capital one triangle may hold reserved at once.",
  },
  {
    section: "risk",
    key: "max_capital_utilization",
    path: "risk.max_capital_utilization",
    label: "Max capital utilization",
    kind: "decimal",
    min: 0,
    minExclusive: true,
    max: 1,
    help: "Max capital utilization must be greater than 0% and at most 100%.",
    effect: "immediate",
      unit: "%", display: "percent", defaultValue: "50", consequence: "Fraction of total capital that may be deployed simultaneously; above it, new cycles are refused.",
  },
  {
    section: "risk",
    key: "max_concurrent_simulations",
    path: "risk.max_concurrent_simulations",
    label: "Max concurrent simulations",
    kind: "int",
    min: 1,
    max: 64,
    help: "Must be between 1 and 64.",
    effect: "immediate",
      defaultValue: "3", consequence: "In-flight paper simulations cap; queue pressure above it refuses new cycles.",
  },
  {
    section: "risk",
    key: "max_book_age_ms",
    path: "risk.max_book_age_ms",
    label: "Max book age (risk)",
    kind: "int",
    min: 100,
    max: 60000,
    help: "Must be between 100 and 60,000 ms.",
    effect: "immediate",
      unit: "ms", defaultValue: "1500", consequence: "The risk gate's own book-age ceiling (tighter than the scanner's by design).",
  },
  {
    section: "risk",
    key: "max_book_age_spread_ms",
    path: "risk.max_book_age_spread_ms",
    label: "Max book age spread",
    kind: "int",
    min: 50,
    max: 60000,
    help: "Must be between 50 and 60,000 ms.",
    effect: "immediate",
      unit: "ms", defaultValue: "750", consequence: "Maximum age difference between a cycle's legs; a fresh leg never trades against a stale one.",
  },
  {
    section: "risk",
    key: "max_slippage_bps",
    path: "risk.max_slippage_bps",
    label: "Max slippage",
    kind: "decimal",
    min: 0,
    help: "Must be 0 or greater.",
    effect: "immediate",
      unit: "bps", defaultValue: "50", consequence: "Three consecutive completed cycles above this open the operator-closed slippage breaker.",
  },
  {
    section: "risk",
    key: "max_price_impact_bps",
    path: "risk.max_price_impact_bps",
    label: "Max price impact",
    kind: "decimal",
    min: 0,
    help: "Must be 0 or greater.",
    effect: "immediate",
      unit: "bps", defaultValue: "30", consequence: "Worst-leg walk impact above this fails the size search.",
  },
  {
    section: "risk",
    key: "max_daily_loss",
    path: "risk.max_daily_loss",
    label: "Max daily loss",
    kind: "decimal",
    min: 0,
    minExclusive: true,
    help: "Must be a positive amount.",
    effect: "immediate",
      unitAsset: "start asset", defaultValue: "200", consequence: "Realized daily loss beyond this opens the daily-loss breaker and halts new cycles.",
  },
  {
    section: "risk",
    key: "max_drawdown",
    path: "risk.max_drawdown",
    label: "Max drawdown",
    kind: "decimal",
    min: 0,
    minExclusive: true,
    max: 1,
    maxExclusive: true,
    help: "Max drawdown must be between 0% and 100% (exclusive).",
    effect: "immediate",
      unit: "%", display: "percent", defaultValue: "5", consequence: "Drawdown fraction beyond this opens the drawdown breaker.",
  },
  {
    section: "risk",
    key: "min_data_quality",
    path: "risk.min_data_quality",
    label: "Min data quality",
    kind: "decimal",
    min: 0,
    max: 1,
    help: "Must be between 0% and 100%.",
    effect: "immediate",
      unit: "%", display: "percent", defaultValue: "50", consequence: "Input quality score below this rejects the opportunity.",
  },
];

export const ALL_FIELDS: FieldSpec[] = [...SCANNER_FIELDS, ...RISK_FIELDS];

export const NOTIFICATION_COOLDOWN_HELP = "Alert cooldown must be between 1 second and 1 hour.";
export const NOTIFICATION_SEVERITIES = ["INFO", "WARNING", "CRITICAL"] as const;
export const NOTIFICATION_CHANNELS = ["web", "telegram"] as const;

export function validateField(spec: FieldSpec, raw: string): string | null {
  const trimmed = raw.trim();
  if (trimmed === "") return "Required.";
  const n = Number(trimmed);
  if (!Number.isFinite(n)) return spec.kind === "int" ? "Must be a whole number." : "Must be a number.";
  if (spec.kind === "int" && !Number.isInteger(n)) return "Must be a whole number.";
  // Percent-display fields hold percents in the form while their bounds
  // are fractions on the wire — compare in the form's space (F11).
  const scale = spec.display === "percent" ? 100 : 1;
  const min = spec.min !== undefined ? spec.min * scale : undefined;
  const max = spec.max !== undefined ? spec.max * scale : undefined;
  if (min !== undefined && (spec.minExclusive ? n <= min : n < min)) return spec.help;
  if (max !== undefined && (spec.maxExclusive ? n >= max : n > max)) return spec.help;
  return null;
}

export function validateCooldown(raw: string): string | null {
  const trimmed = raw.trim();
  if (trimmed === "") return "Required.";
  const n = Number(trimmed);
  if (!Number.isFinite(n) || !Number.isInteger(n)) return "Must be a whole number of seconds.";
  if (n < 1 || n > 3600) return NOTIFICATION_COOLDOWN_HELP;
  return null;
}

// cloneParams deep-clones via JSON round-trip. Params is plain JSON data
// (decimal strings, numbers, a string-array map) so this is lossless and,
// critically, leaves every untouched leaf byte-identical — re-serializing
// an unedited decimal string through a number would round-trip it
// differently (e.g. "0.50" -> "0.5") and manufacture a diff the operator
// never asked for, which can silently trip the risk->ADMIN permission
// gate for an OPERATOR editing only scanner/notifications fields.
export function cloneParams(p: StrategyParams): StrategyParams {
  return JSON.parse(JSON.stringify(p)) as StrategyParams;
}

export function getPath(obj: unknown, path: string): unknown {
  return path.split(".").reduce<unknown>((acc, key) => {
    if (acc !== null && typeof acc === "object") return (acc as Record<string, unknown>)[key];
    return undefined;
  }, obj);
}

// setPath clones the whole document and overwrites exactly one leaf,
// leaving every other field — including its exact string representation
// — untouched.
export function setPath(obj: StrategyParams, path: string, value: unknown): StrategyParams {
  const clone = cloneParams(obj);
  const parts = path.split(".");
  const last = parts[parts.length - 1]!;
  let cur = clone as unknown as Record<string, unknown>;
  for (let i = 0; i < parts.length - 1; i++) {
    cur = cur[parts[i]!] as Record<string, unknown>;
  }
  cur[last] = value;
  return clone;
}

// fieldPathFromError finds a known "section.field" token inside a backend
// error message. The service double-prefixes ("strategy: invalid
// parameters: strategy: risk.max_drawdown out of (0,1)"), so this
// searches for the substring rather than assuming a fixed position.
export function fieldPathFromError(message: string): string | null {
  const candidates = [...ALL_FIELDS.map((f) => f.path), "notifications.cooldown_seconds"];
  for (const p of candidates) {
    if (message.includes(p)) return p;
  }
  return null;
}
