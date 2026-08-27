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
  if (spec.min !== undefined && (spec.minExclusive ? n <= spec.min : n < spec.min)) return spec.help;
  if (spec.max !== undefined && (spec.maxExclusive ? n >= spec.max : n > spec.max)) return spec.help;
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
