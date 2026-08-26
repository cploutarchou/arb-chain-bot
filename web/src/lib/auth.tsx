"use client";

// Session context: /auth/me on mount recovers the principal and CSRF
// token after reloads; unauthenticated visitors are sent to /login.
// RBAC here is presentation only — the backend enforces every action.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { usePathname, useRouter } from "next/navigation";
import { api, setCsrfToken, type Me } from "@/lib/api/client";

type AuthState =
  | { kind: "loading" }
  | { kind: "anonymous" }
  | { kind: "authenticated"; me: Me };

interface AuthContextValue {
  state: AuthState;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ kind: "loading" });
  const router = useRouter();
  const pathname = usePathname();

  useEffect(() => {
    let cancelled = false;
    api.auth
      .me()
      .then((me) => {
        if (cancelled) return;
        if (me.csrf_token) setCsrfToken(me.csrf_token);
        setState({ kind: "authenticated", me });
      })
      .catch(() => !cancelled && setState({ kind: "anonymous" }));
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (state.kind === "anonymous" && pathname !== "/login") {
      router.replace("/login");
    }
  }, [state.kind, pathname, router]);

  const login = useCallback(async (email: string, password: string) => {
    const res = await api.auth.login(email, password);
    setCsrfToken(res.csrf_token);
    const me = await api.auth.me();
    setState({ kind: "authenticated", me });
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.auth.logout();
    } finally {
      setCsrfToken("");
      setState({ kind: "anonymous" });
    }
  }, []);

  return <AuthContext.Provider value={{ state, login, logout }}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth outside AuthProvider");
  return ctx;
}

// can mirrors the backend RBAC matrix for showing/hiding controls; the
// backend remains the actual gate (ADMIN holds every permission).
export function can(role: string | undefined, perm: string): boolean {
  const operator = new Set([
    "paper:control",
    "scanner:config",
    "ai:approve",
    "alerts:ack",
    "view:audit",
    "recordings:control",
    "campaigns:run",
  ]);
  if (role === "ADMIN") return true;
  if (role === "OPERATOR") return operator.has(perm) || perm.startsWith("view:") || perm === "reports:view";
  if (role === "VIEWER") return perm.startsWith("view:") || perm === "reports:view";
  return false;
}
