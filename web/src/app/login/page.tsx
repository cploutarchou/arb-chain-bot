"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { ApiError } from "@/lib/api/client";

export default function LoginPage() {
  const { login } = useAuth();
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await login(email, password);
      router.replace("/overview");
    } catch (err: unknown) {
      setError(err instanceof ApiError && err.status === 429 ? "Too many attempts; wait a minute." : "Login failed.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center">
      <form
        onSubmit={submit}
        className="w-80 rounded border border-[var(--border)] bg-[var(--bg-panel)] p-6"
      >
        <h1 className="text-sm font-semibold tracking-wide">ARB CONSOLE</h1>
        <p className="mb-4 mt-1 text-[11px] uppercase tracking-wider text-[var(--text-dim)]">
          paper trading only — live execution permanently disabled
        </p>
        <label className="mb-3 block text-[12px]">
          <span className="text-[var(--text-dim)]">Email</span>
          <input
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1.5 text-sm outline-none focus:border-[var(--accent)]"
          />
        </label>
        <label className="mb-4 block text-[12px]">
          <span className="text-[var(--text-dim)]">Password</span>
          <input
            type="password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1.5 text-sm outline-none focus:border-[var(--accent)]"
          />
        </label>
        {error && <p className="mb-3 text-[12px] text-[var(--critical)]">{error}</p>}
        <button
          type="submit"
          disabled={busy}
          className="w-full rounded border border-[var(--accent)] px-3 py-1.5 text-sm font-medium text-[var(--accent)] hover:bg-[var(--accent)] hover:text-black disabled:opacity-50"
        >
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </div>
  );
}
