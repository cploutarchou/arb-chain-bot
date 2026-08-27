// Shared client-side diff helpers for confirmation dialogs (Strategies
// apply/rollback) and the Replay config comparator. Values are rendered
// verbatim (JSON.stringify of whatever the backend returned) — this is
// presentation only, never a financial/risk recomputation.

export function flatten(obj: Record<string, unknown>, prefix = ""): Map<string, string> {
  const out = new Map<string, string>();
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v !== null && typeof v === "object" && !Array.isArray(v)) {
      for (const [ck, cv] of flatten(v as Record<string, unknown>, key)) out.set(ck, cv);
    } else {
      out.set(key, JSON.stringify(v));
    }
  }
  return out;
}

export interface DiffRow {
  path: string;
  before: string;
  after: string;
}

// diffParams flattens two params objects to dotted paths and returns only
// the leaves that differ, sorted by path.
export function diffParams(before: Record<string, unknown>, after: Record<string, unknown>): DiffRow[] {
  const fa = flatten(before);
  const fb = flatten(after);
  const paths = new Set([...fa.keys(), ...fb.keys()]);
  const rows: DiffRow[] = [];
  for (const p of [...paths].sort()) {
    const x = fa.get(p) ?? "—";
    const y = fb.get(p) ?? "—";
    if (x !== y) rows.push({ path: p, before: x, after: y });
  }
  return rows;
}

// effectFor tags a dotted param path with its hot-swap timing. Only
// scanner.workers is documented (params.go:40) as applying at component
// start rather than immediately on the running scanner — every other
// field hot-swaps.
export function effectFor(path: string): "immediate" | "on restart" {
  return path === "scanner.workers" ? "on restart" : "immediate";
}

// fmtDiffValue renders one side of a backend-supplied ConfigVersion.diff
// leaf (Record<string, {old,new}>) verbatim — never just the changed key.
export function fmtDiffValue(v: unknown): string {
  if (v === null || v === undefined) return "—";
  if (typeof v === "string") return v;
  return JSON.stringify(v);
}
