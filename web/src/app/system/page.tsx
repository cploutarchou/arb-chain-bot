"use client";

import { useEffect, useState } from "react";
import { api, type SystemStatus } from "@/lib/api/client";
import { ConsoleShell } from "@/components/ConsoleShell";

export default function SystemHealthPage() {
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.system
      .status()
      .then(setStatus)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : "error"));
  }, []);

  return (
    <ConsoleShell active="System Health">
      <h1 className="mb-4 text-lg font-semibold">System Health</h1>
      {error && (
        <p className="text-sm text-[var(--critical)]">Backend unreachable: {error}</p>
      )}
      {status && (
        <pre className="max-w-2xl overflow-x-auto rounded border border-[var(--border)] bg-[var(--bg-panel)] p-4 text-xs">
          {JSON.stringify(status, null, 2)}
        </pre>
      )}
      <p className="mt-6 max-w-2xl text-xs text-[var(--text-dim)]">
        Full health payload (per-feed state, queue depths, goroutines, GC, DB
        pool, clock quality) arrives with observability task T-035.
      </p>
    </ConsoleShell>
  );
}
