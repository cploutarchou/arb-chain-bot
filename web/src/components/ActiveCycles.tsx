"use client";

// ActiveCycles is the Paper page's live-cycle monitor (audit F6): what
// is executing right now, at which leg, for how long, and against what
// expected outcome — polled fast enough (1 s) for the elapsed read to
// tick, from the engine's own in-flight registry
// (GET /api/v1/paper/active). Every figure is the backend's: expected
// net is the plan's own number, never a live re-quote; realized PnL
// exists only on the settled cycle and stays in the cycles table below.
//
// A 404 paper_absent (mode is not PAPER) renders nothing here — the
// page's Engine section already explains the absence, and a second copy
// of the explanation would only repeat it.

import { useEffect, useState } from "react";
import { api, type PaperActiveCycle } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { Badge, Stat, fmtTime, type Tone } from "@/components/ui";
import { fmtDurationMs } from "@/lib/feedState";

const STAGE_TONE: Record<string, Tone> = {
  PENDING: "dim",
  SUBMITTED: "warn",
  FILLED: "ok",
  FAILED: "bad",
};

function LegBadge({
  legNo,
  market,
  side,
  stage,
}: {
  legNo: number;
  market: string;
  side: string;
  stage: string;
}) {
  return (
    <Badge tone={STAGE_TONE[stage] ?? "dim"}>
      {legNo} {market} {side} · {stage}
    </Badge>
  );
}

function CycleRow({ cycle, now }: { cycle: PaperActiveCycle; now: number }) {
  const elapsed = now - new Date(cycle.started_at).getTime();
  return (
    <div
      className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3"
      data-testid="active-cycle"
    >
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 text-[13px]">
        <span className="font-mono text-[12px] text-[var(--text-dim)]">
          {cycle.cycle_id}
        </span>
        <span className="font-medium">{cycle.triangle_id}</span>
        <span className="text-[var(--text-dim)]">
          deployed{" "}
          <strong className="text-[var(--text)]">
            {cycle.input} {cycle.start_asset}
          </strong>
        </span>
        <span className="text-[var(--text-dim)]">
          expected <strong className="text-[var(--text)]">{cycle.expected_net_bps} bps</strong>{" "}
          ({cycle.expected_profit} {cycle.start_asset})
        </span>
        <span
          className="ml-auto font-mono text-[12px] text-[var(--text-dim)]"
          data-testid="active-cycle-elapsed"
          title={`started ${fmtTime(cycle.started_at)}`}
        >
          {fmtDurationMs(elapsed)} elapsed
        </span>
      </div>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {(cycle.legs ?? []).map((leg) => (
          <LegBadge
            key={leg.leg_no}
            legNo={leg.leg_no}
            market={leg.market}
            side={leg.side}
            stage={leg.stage}
          />
        ))}
      </div>
    </div>
  );
}

export function ActiveCycles({
  running,
  activeCount,
}: {
  running: boolean | undefined;
  activeCount: number | undefined;
}) {
  const state = usePoll(() => api.paper.active(), 1000);
  const [now, setNow] = useState(() => Date.now());

  // The elapsed read ticks on the client clock between polls; the data
  // itself still only moves at the poll cadence.
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  if (state.kind === "error" && state.code === "paper_absent") return null;

  const cycles = state.kind === "ready" ? state.data.cycles ?? [] : [];

  return (
    <div>
      <div className="mb-3 grid max-w-3xl grid-cols-1 gap-3 sm:grid-cols-3">
        <Stat
          label="In flight"
          value={state.kind === "ready" ? cycles.length : "…"}
          tone={
            state.kind === "ready" && cycles.length === 0
              ? "dim"
              : undefined
          }
        />
        <Stat
          label="Engine"
          value={state.kind === "ready" ? (state.data.running ? "RUNNING" : "PAUSED") : "…"}
          tone={state.kind === "ready" && !state.data.running ? "warn" : "ok"}
        />
        <Stat label="Active sims (engine count)" value={activeCount ?? "—"} />
      </div>
      {state.kind === "loading" && (
        <p className="text-sm text-[var(--text-dim)]">Loading live cycles…</p>
      )}
      {state.kind === "error" && (
        <p className="text-sm text-[var(--critical)]" role="alert">
          Live-cycle view unavailable (HTTP {state.status ?? "?"}):{" "}
          {state.message}
        </p>
      )}
      {state.kind === "ready" && cycles.length === 0 && (
        <p className="text-sm text-[var(--text-dim)]">
          No cycle is executing right now —{" "}
          {state.data.running
            ? "the engine is running; new qualified opportunities appear here the moment capital is reserved."
            : "the engine is paused, so no new cycles start until it resumes."}
        </p>
      )}
      {state.kind === "ready" && cycles.length > 0 && (
        <div className="flex flex-col gap-2">
          {cycles.map((c) => (
            <CycleRow key={c.cycle_id} cycle={c} now={now} />
          ))}
        </div>
      )}
      {state.kind === "ready" && running === false && cycles.length > 0 && (
        <p className="mt-2 text-[12px] text-[var(--text-dim)]">
          The engine is paused — these cycles were already in flight and
          settle to their own outcome.
        </p>
      )}
    </div>
  );
}
