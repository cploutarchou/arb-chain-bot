import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { can } from "@/lib/auth";

// `can()` decides which controls and navigation entries a role is shown.
// It is presentation only — the backend re-checks every route — but when
// it disagrees with the backend the user is handed a link that 403s, and
// is told nothing about why.
//
// These tests read internal/auth/rbac.go and assert the frontend matrix
// agrees with it, so the two cannot drift silently. That drift is exactly
// what happened: `can()` matched any permission starting with "view:" for
// VIEWER, which silently included `view:audit` — a permission rbac.go
// grants only to OPERATOR and ADMIN.

const RBAC_GO = join(__dirname, "..", "..", "internal", "auth", "rbac.go");

// Parse the Go matrix: each role's setOf(...) block lists Perm* constants,
// and the const block maps those names to their wire strings.
function backendMatrix(): Record<string, Set<string>> {
  const src = readFileSync(RBAC_GO, "utf8");
  const constToWire = new Map<string, string>();
  for (const m of src.matchAll(/(Perm\w+)\s+Permission\s*=\s*"([^"]+)"/g)) {
    if (m[1] && m[2]) constToWire.set(m[1], m[2]);
  }
  const out: Record<string, Set<string>> = {};
  for (const m of src.matchAll(/(RoleViewer|RoleOperator|RoleAdmin):\s*setOf\(([\s\S]*?)\),\n/g)) {
    const role = m[1]!.replace("Role", "").toUpperCase();
    const perms = new Set<string>();
    for (const c of m[2]!.matchAll(/Perm\w+/g)) {
      const wire = constToWire.get(c[0]);
      if (wire) perms.add(wire);
    }
    out[role] = perms;
  }
  return out;
}

test.describe("can() agrees with internal/auth/rbac.go", () => {
  test("the Go matrix parses into three roles", () => {
    const m = backendMatrix();
    expect(Object.keys(m).sort()).toEqual(["ADMIN", "OPERATOR", "VIEWER"]);
    expect(m.VIEWER!.size).toBeGreaterThan(3);
    expect(m.OPERATOR!.size).toBeGreaterThan(m.VIEWER!.size);
  });

  test("no role is shown a permission the backend does not grant it", () => {
    // The direction that matters: showing a control the backend refuses
    // hands the user a link that 403s with no explanation.
    const m = backendMatrix();
    const every = new Set<string>([...m.VIEWER!, ...m.OPERATOR!, ...m.ADMIN!]);
    const overreach: string[] = [];
    for (const role of ["VIEWER", "OPERATOR"]) {
      for (const perm of every) {
        if (can(role, perm) && !m[role]!.has(perm)) {
          overreach.push(`${role} is shown ${perm}`);
        }
      }
    }
    expect(overreach, overreach.join("; ")).toEqual([]);
  });

  test("no role is denied a permission the backend does grant it", () => {
    // The opposite direction: hiding something the backend would allow
    // silently removes a capability.
    const m = backendMatrix();
    const missing: string[] = [];
    for (const role of ["VIEWER", "OPERATOR", "ADMIN"]) {
      for (const perm of m[role]!) {
        if (!can(role, perm)) missing.push(`${role} is denied ${perm}`);
      }
    }
    expect(missing, missing.join("; ")).toEqual([]);
  });

  test("the specific regression: a VIEWER is not shown the audit log", () => {
    // rbac.go grants PermViewAudit to OPERATOR and ADMIN only. `can()`
    // used to return true for any "view:" prefix, so the navigation
    // rendered Audit log as a live link for a VIEWER, who then got a 403.
    expect(can("VIEWER", "view:audit")).toBe(false);
    expect(can("OPERATOR", "view:audit")).toBe(true);
    expect(can("ADMIN", "view:audit")).toBe(true);
    // And the prefix shortcut is gone: an invented view: permission is
    // not granted to anyone but ADMIN.
    expect(can("VIEWER", "view:not-a-real-permission")).toBe(false);
    expect(can("OPERATOR", "view:not-a-real-permission")).toBe(false);
  });

  test("an unknown or missing role holds nothing", () => {
    for (const role of [undefined, "", "GUEST", "admin"]) {
      expect(can(role as string | undefined, "view:dashboard")).toBe(false);
    }
  });

  test("the two settings tiers resolve as the backend does", () => {
    // screener:config and risk:config are ADMIN-only (PermScreenerConfig,
    // PermRiskConfig); scanner:config is OPERATOR+ and is a DIFFERENT
    // permission governing triangular strategy config.
    expect(can("ADMIN", "screener:config")).toBe(true);
    expect(can("OPERATOR", "screener:config")).toBe(false);
    expect(can("ADMIN", "risk:config")).toBe(true);
    expect(can("OPERATOR", "risk:config")).toBe(false);
    expect(can("OPERATOR", "scanner:config")).toBe(true);
    expect(can("VIEWER", "scanner:config")).toBe(false);
  });
});
