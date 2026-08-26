"use client";

import { useEffect, useState } from "react";
import { api, type SystemStatus, ApiError } from "@/lib/api/client";
import { ConsoleShell } from "@/components/ConsoleShell";

type LoadState =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "ready"; status: SystemStatus };

export default function OverviewPage() {
  const [state, setState] = useState<LoadState>({ kind: "loading" });

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api.system
        .status()
        .then((status) => !cancelled && setState({ kind: "ready", status }))
        .catch((err: unknown) => {
          if (cancelled) return;
          const message =
            err instanceof ApiError ? err.message : "Backend unreachable";
          setState({ kind: "error", message });
        });
    load();
    const t = setInterval(load, 5000); // replaced by the WS health topic in T-030
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, []);

  return (
    <ConsoleShell active="Overview">
      <h1 className="mb-4 text-lg font-semibold">Overview</h1>

      {state.kind === "loading" && (
        <p className="text-sm text-[var(--text-dim)]">Loading system status…</p>
      )}

      {state.kind === "error" && (
        <div className="rounded border border-[var(--critical)] bg-[var(--bg-panel)] p-4 text-sm">
          <span className="font-medium text-[var(--critical)]">Degraded:</span>{" "}
          {state.message}
        </div>
      )}

      {state.kind === "ready" && (
        <div className="grid max-w-3xl grid-cols-2 gap-3 md:grid-cols-4">
          <Stat label="Mode" value={state.status.mode} />
          <Stat label="Version" value={state.status.version} />
          <Stat label="Uptime" value={`${state.status.uptime_sec}s`} />
          <Stat
            label="Components"
            value={state.status.components.join(", ") || "none"}
          />
        </div>
      )}

      <p className="mt-8 max-w-2xl text-xs leading-relaxed text-[var(--text-dim)]">
        Dashboard sections (today&apos;s opportunities, paper P&amp;L, exchange
        health, top triangles) land with their backing services — see
        docs/MASTER_PLAN.md. This console never displays numbers the backend
        has not produced.
      </p>
    </ConsoleShell>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3">
      <div className="text-[10px] uppercase tracking-wider text-[var(--text-dim)]">
        {label}
      </div>
      <div className="mt-1 truncate text-sm" title={value}>
        {value}
      </div>
    </div>
  );
}
