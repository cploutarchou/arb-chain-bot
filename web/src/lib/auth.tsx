"use client";

// Session context: /auth/me on mount recovers the principal and CSRF
// token after reloads; unauthenticated visitors are sent to /login.
// RBAC here is presentation only — the backend enforces every action.
// Since T-081/T-082 the recovered principal also carries the
// organisation, the membership role, the platform_admin flag and the
// organisation's resolved entitlements (packages.md §3) — the console
// only reads these, every limit stays enforced server-side.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { usePathname, useRouter } from "next/navigation";
import {
  api,
  setCsrfToken,
  type Entitlements,
  type Me,
} from "@/lib/api/client";
import { onRiskAckRequired } from "@/lib/errorBus";

type AuthState =
  | { kind: "loading" }
  | { kind: "anonymous" }
  | { kind: "authenticated"; me: Me };

interface AuthContextValue {
  state: AuthState;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  // refreshMe re-fetches /me (used after a risk-ack accept and after any
  // entitlement/subscription change) without a full page reload.
  refreshMe: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ kind: "loading" });
  const router = useRouter();
  const pathname = usePathname();

  const loadMe = useCallback(async () => {
    const me = await api.auth.me();
    if (me.csrf_token) setCsrfToken(me.csrf_token);
    setState({ kind: "authenticated", me });
    return me;
  }, []);

  useEffect(() => {
    let cancelled = false;
    loadMe().catch(() => !cancelled && setState({ kind: "anonymous" }));
    return () => {
      cancelled = true;
    };
  }, [loadMe]);

  useEffect(() => {
    if (state.kind === "anonymous" && pathname !== "/login") {
      router.replace("/login");
    }
  }, [state.kind, pathname, router]);

  // Any API call anywhere in the console can come back 403
  // risk_ack_required (the disclosure version can be bumped while a
  // session is open) — errorBus tells us to re-fetch /me so the
  // blocking gate renders even if the initial /me load raced it.
  useEffect(() => {
    return onRiskAckRequired(() => {
      loadMe().catch(() => {
        // The re-fetch itself failing (e.g. session expired in the
        // meantime) is handled by the normal 401 → anonymous path the
        // next time a request runs; nothing to do here.
      });
    });
  }, [loadMe]);

  const login = useCallback(
    async (email: string, password: string) => {
      const res = await api.auth.login(email, password);
      setCsrfToken(res.csrf_token);
      await loadMe();
    },
    [loadMe],
  );

  const logout = useCallback(async () => {
    try {
      await api.auth.logout();
    } finally {
      setCsrfToken("");
      setState({ kind: "anonymous" });
    }
  }, []);

  const refreshMe = useCallback(async () => {
    await loadMe();
  }, [loadMe]);

  return (
    <AuthContext.Provider value={{ state, login, logout, refreshMe }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth outside AuthProvider");
  return ctx;
}

// can mirrors the backend RBAC matrix for showing/hiding controls; the
// backend remains the actual gate (ADMIN holds every permission).
//
// VIEWER is enumerated rather than matched on a `view:` prefix. The
// prefix was wrong: `internal/auth/rbac.go:46-50` grants RoleViewer
// PermViewDashboard, PermViewOpportunity, PermViewPortfolio,
// PermViewRisk, PermViewSystem, PermReportView and PermScreenerView —
// but NOT PermViewAudit, which is OPERATOR+ only (rbac.go:53,60). So
// `can("VIEWER", "view:audit")` returned true while the backend returns
// 403. The previous shell papered over it by special-casing the "Audit
// Log" label; keying navigation on routes instead of labels removed that
// patch and exposed the underlying bug, which is fixed here at the root.
const VIEWER_PERMS = new Set([
  "view:dashboard",
  "view:opportunities",
  "view:portfolio",
  "view:risk",
  "view:system",
  "reports:view",
  "screener:view",
]);

const OPERATOR_PERMS = new Set([
  ...VIEWER_PERMS,
  "view:audit",
  "paper:control",
  "scanner:config",
  "ai:approve",
  "alerts:ack",
  "recordings:control",
  "campaigns:run",
  "reports:generate",
]);

export function can(role: string | undefined, perm: string): boolean {
  if (role === "ADMIN") return true;
  if (role === "OPERATOR") return OPERATOR_PERMS.has(perm);
  if (role === "VIEWER") return VIEWER_PERMS.has(perm);
  return false;
}

// ---- Entitlement helpers (packages.md §3) ---------------------------------
// A dot-path key into the Entitlements document (typed below so
// useEntitlement("api.enabled") is boolean and
// useEntitlement("alerts.channels") is string[], never `unknown`).
export type EntitlementKey =
  | "venues.screener_max"
  | "venues.screener_tiers"
  | "venues.screener_fixed"
  | "venues.triangular_max"
  | "venues.dex_enabled"
  | "venues.perps_enabled"
  | "rules.max_active"
  | "rules.templates_max"
  | "rules.min_refresh_s"
  | "rules.kinds"
  | "alerts.channels"
  | "alerts.per_day"
  | "alerts.telegram_destinations_max"
  | "alerts.min_cooldown_s"
  | "auto_paper.strategies"
  | "auto_paper.max_open_positions"
  | "auto_paper.ledgers_max"
  | "auto_paper.max_size_quote"
  | "api.enabled"
  | "api.scopes"
  | "api.rate_per_min"
  | "api.burst"
  | "api.keys_max"
  | "api.streaming"
  | "history.retention_days"
  | "history.export_formats"
  | "history.export_scheduled"
  | "history.reports"
  | "seats.max"
  | "seats.roles";

interface EntitlementValueMap {
  "venues.screener_max": number;
  "venues.screener_tiers": string[];
  "venues.screener_fixed": string[];
  "venues.triangular_max": number;
  "venues.dex_enabled": boolean;
  "venues.perps_enabled": boolean;
  "rules.max_active": number;
  "rules.templates_max": number;
  "rules.min_refresh_s": number;
  "rules.kinds": string[];
  "alerts.channels": string[];
  "alerts.per_day": number;
  "alerts.telegram_destinations_max": number;
  "alerts.min_cooldown_s": number;
  "auto_paper.strategies": string[];
  "auto_paper.max_open_positions": number;
  "auto_paper.ledgers_max": number;
  "auto_paper.max_size_quote": string;
  "api.enabled": boolean;
  "api.scopes": string[];
  "api.rate_per_min": number;
  "api.burst": number;
  "api.keys_max": number;
  "api.streaming": boolean;
  "history.retention_days": number;
  "history.export_formats": string[];
  "history.export_scheduled": boolean;
  "history.reports": string;
  "seats.max": number;
  "seats.roles": string[];
}

function readEntitlement<K extends EntitlementKey>(
  ent: Entitlements,
  key: K,
): EntitlementValueMap[K] {
  const [group, field] = key.split(".") as [keyof Entitlements, string];
  const section = ent[group] as unknown as Record<string, unknown>;
  return section[field] as EntitlementValueMap[K];
}

// useEntitlement reads one field of the organisation's resolved
// entitlements document. Returns undefined while auth is still loading
// (never a stale/default value — a gated control must render nothing
// gated on first paint rather than flash "locked" then unlock, and never
// silently allow a control that would 403 on the backend). Returns
// undefined for an anonymous session (nothing to gate on a login page).
export function useEntitlement<K extends EntitlementKey>(
  key: K,
): EntitlementValueMap[K] | undefined {
  const { state } = useAuth();
  return useMemo(() => {
    if (state.kind !== "authenticated") return undefined;
    return readEntitlement(state.me.entitlements, key);
  }, [state, key]);
}

// isUnlimited: venues.screener_max and rules.templates_max document
// "-1 = unlimited" (packages.md §3.1) — centralised here so no call site
// naively does `count >= max`.
export function isUnlimited(limit: number | undefined): boolean {
  return limit === -1;
}

// withinLimit: true when `count` has not yet reached `limit`, accounting
// for the -1-means-unlimited convention. `limit === undefined` (auth
// still loading) is treated as "not yet known" — never gate optimistically.
export function withinLimit(
  count: number,
  limit: number | undefined,
): boolean | undefined {
  if (limit === undefined) return undefined;
  if (isUnlimited(limit)) return true;
  return count < limit;
}
