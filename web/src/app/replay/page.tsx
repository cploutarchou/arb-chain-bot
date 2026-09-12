"use client";

// Console-driven replay runs (BL-17): a single-flight background job
// that drives the real scanner over one recording's segments, optionally
// pinned to a persisted strategy version. Recorded-sessions management
// (start/stop, the full sessions table) lives on Campaigns — this page
// only needs a recording to pick from, so it links there instead of
// repeating that table.

import { useEffect, useState } from "react";
import Link from "next/link";
import { api, ApiError, isNotReady, request, type ReplayRun } from "@/lib/api/client";
import { usePoll, type PollState } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { connectHub, type HubMessage } from "@/lib/ws";
import { flatten } from "@/lib/diff";
import { ConsoleShell } from "@/components/ConsoleShell";
import { OutcomeBadge } from "@/components/OutcomeBadge";
import { Await, Badge, Button, ErrorBox, Loading, PageTitle, Section, Table, fmtTime } from "@/components/ui";

interface ConfigSnapshotFull {
  version: number;
  params: Record<string, unknown>;
}

interface ReplaysTopicMsg {
  kind?: "replay_run";
  run?: ReplayRun;
  runs?: ReplayRun[];
}

function statusTone(status: ReplayRun["status"]): "ok" | "warn" | "bad" | "dim" {
  if (status === "done") return "ok";
  if (status === "running") return "warn";
  if (status === "failed") return "bad";
  return "dim"; // queued
}

// Overlay live WS runs onto the polled list, same merge convention as
// Campaigns: polled rows take the live version when present, and a run
// that only exists on the socket (just POSTed) is prepended.
function mergeRuns(polled: ReplayRun[], live: Record<string, ReplayRun>): ReplayRun[] {
  const seen = new Set(polled.map((r) => r.id));
  const merged = polled.map((r) => live[r.id] ?? r);
  const extra = Object.values(live).filter((r) => !seen.has(r.id));
  return [...extra, ...merged];
}

export default function ReplayPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayRun = can(role, "campaigns:run"); // PermCampaignRun gates POST /api/v1/replays

  const [refresh, setRefresh] = useState(0);
  const recordings = usePoll(() => api.recordings.list(), 15000);
  const versions = usePoll(() => api.config.versions(50), 15000);
  const runsState = usePoll(() => api.replays.list(25), 5000, [refresh]);

  const [wsStatus, setWsStatus] = useState<"connecting" | "open" | "closed">("connecting");
  const [wsRuns, setWsRuns] = useState<Record<string, ReplayRun>>({});

  useEffect(() => {
    return connectHub(["replays"], {
      onStatus: (s) => {
        setWsStatus(s);
        if (s === "closed") setWsRuns((prev) => (Object.keys(prev).length ? {} : prev));
      },
      onMessage: (msg: HubMessage) => {
        if (msg.topic !== "replays") return;
        const data = msg.data as ReplaysTopicMsg;
        if (msg.snapshot) {
          const runs = data.runs ?? [];
          setWsRuns((prev) => {
            const next = { ...prev };
            for (const run of runs) next[run.id] = run;
            return next;
          });
          return;
        }
        if (data.kind === "replay_run" && data.run) {
          const run = data.run;
          setWsRuns((prev) => ({ ...prev, [run.id]: run }));
        }
      },
    });
  }, []);

  const polledRuns = runsState.kind === "ready" ? (runsState.data.runs ?? []) : [];
  const mergedRuns = mergeRuns(polledRuns, wsRuns);
  const runInProgress = mergedRuns.some((r) => r.status === "queued" || r.status === "running");

  const [selRecording, setSelRecording] = useState("");
  const [selConfigVersion, setSelConfigVersion] = useState<number | "">("");
  const [speed, setSpeed] = useState("1");
  const [submitting, setSubmitting] = useState(false);
  // retryable mirrors campaigns' handling of a 503 not_ready right after
  // boot (the replay runner's background context isn't wired yet).
  const [runMsg, setRunMsg] = useState<{ ok: boolean; text: string; retryable?: boolean } | null>(null);
  // detailID/detailState split loading/error/ready the same way usePoll
  // does (audit F7) — a failed "View" used to silently clear the result
  // section instead of saying the fetch failed.
  const [detailID, setDetailID] = useState<string | null>(null);
  const [detailState, setDetailState] = useState<PollState<ReplayRun>>({ kind: "loading" });

  const submitRun = async () => {
    setRunMsg(null);
    if (!selRecording) {
      setRunMsg({ ok: false, text: "Select a recording." });
      return;
    }
    setSubmitting(true);
    try {
      const res = await api.replays.start({
        recording: selRecording,
        config_version: selConfigVersion === "" ? undefined : selConfigVersion,
        speed: speed.trim() ? Number(speed) : undefined,
      });
      setRunMsg({ ok: true, text: `Replay ${res.run.id} queued.` });
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setRunMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Replay launch failed.",
        retryable: isNotReady(err),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const viewRun = async (id: string) => {
    setDetailID(id);
    setDetailState({ kind: "loading" });
    try {
      const res = await api.replays.get(id);
      setDetailState({ kind: "ready", data: res.run, lastOkAt: Date.now() });
    } catch (err: unknown) {
      setDetailState(
        err instanceof ApiError
          ? { kind: "error", message: err.message, status: err.status, code: err.apiError?.code }
          : { kind: "error", message: "Backend unreachable" },
      );
    }
  };

  // Config comparison (any two persisted strategy versions) — the one
  // part of the old page that was not a duplicate of anything else.
  const [a, setA] = useState<number | "">("");
  const [b, setB] = useState<number | "">("");
  const [diff, setDiff] = useState<{ path: string; a: string; b: string }[] | null>(null);
  const [diffErr, setDiffErr] = useState("");
  const compare = async () => {
    if (a === "" || b === "") return;
    setDiffErr("");
    try {
      const [va, vb] = await Promise.all([
        request<ConfigSnapshotFull>(`/api/v1/config/version/${a}`),
        request<ConfigSnapshotFull>(`/api/v1/config/version/${b}`),
      ]);
      const fa = flatten(va.params);
      const fb = flatten(vb.params);
      const paths = new Set([...fa.keys(), ...fb.keys()]);
      const rows: { path: string; a: string; b: string }[] = [];
      for (const p of [...paths].sort()) {
        const x = fa.get(p) ?? "—";
        const y = fb.get(p) ?? "—";
        if (x !== y) rows.push({ path: p, a: x, b: y });
      }
      setDiff(rows);
    } catch {
      setDiffErr("Comparison failed (unknown version?).");
      setDiff(null);
    }
  };

  return (
    <ConsoleShell>
      <PageTitle>Replay &amp; Backtesting</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        A replay drives the exact same decode → validate → apply → evaluate path as live, over one
        closed recording&apos;s segments; the same recording + config + seed produces an identical
        decision log. Recordings are captured and managed from{" "}
        <Link href="/campaigns" className="text-[var(--accent)] underline">
          Campaigns → Recorder
        </Link>
        . (WS {wsStatus === "open" ? "connected" : wsStatus})
      </p>

      <Section title="Run a replay">
        <Await state={recordings} what="recordings">
          {(r) => {
            const recs = r.recordings ?? [];
            const closed = recs.filter((rec) => rec.ended_at);
            if (recs.length === 0) {
              return (
                <p className="text-sm text-[var(--text-dim)]">
                  No recordings yet. Start one from{" "}
                  <Link href="/campaigns" className="text-[var(--accent)] underline">
                    Campaigns → Recorder
                  </Link>
                  .
                </p>
              );
            }
            return (
              <div className="max-w-2xl space-y-3 text-[13px]">
                <label className="flex items-center gap-2">
                  <span className="w-44 shrink-0 text-[var(--text-dim)]">Recording</span>
                  <select
                    value={selRecording}
                    onChange={(e) => setSelRecording(e.target.value)}
                    className="flex-1 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                  >
                    <option value="">select a closed recording…</option>
                    {closed.map((rec) => (
                      <option key={rec.id} value={rec.id}>
                        {rec.id} ({rec.exchange_id})
                      </option>
                    ))}
                  </select>
                </label>
                {closed.length === 0 && (
                  <p className="text-[12px] text-[var(--warn)]">
                    No closed recordings yet — a replay needs a session with an end time. Stop the active
                    recording on Campaigns first.
                  </p>
                )}
                <label className="flex items-center gap-2">
                  <span className="w-44 shrink-0 text-[var(--text-dim)]">Config version (optional)</span>
                  <select
                    value={selConfigVersion}
                    onChange={(e) => setSelConfigVersion(e.target.value === "" ? "" : Number(e.target.value))}
                    className="flex-1 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                  >
                    <option value="">use whatever is live when the run starts</option>
                    {versions.kind === "ready" &&
                      (versions.data ?? []).map((v) => (
                        <option key={v.version} value={v.version}>
                          v{v.version}
                          {v.active ? " (active)" : ""}
                        </option>
                      ))}
                  </select>
                </label>
                <label className="flex items-center gap-2">
                  <span className="w-44 shrink-0 text-[var(--text-dim)]">Speed</span>
                  <input
                    value={speed}
                    onChange={(e) => setSpeed(e.target.value)}
                    type="number"
                    step="0.1"
                    className="w-24 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                  />
                </label>
                <p className="max-w-lg text-[12px] text-[var(--text-dim)]">
                  Speed has no effect on execution yet: a replay steps a deterministic, single-threaded
                  discrete-event backtest to the next recorded frame or latency deadline, not to
                  wall-clock time, so there is no &quot;pace&quot; to scale. It is accepted and persisted
                  for the audit trail and forward compatibility only.
                </p>
                <div className="flex items-center gap-2">
                  <Button onClick={submitRun} disabled={!mayRun || submitting || runInProgress || !selRecording}>
                    {submitting ? "Launching…" : "Run replay"}
                  </Button>
                  {runInProgress && (
                    <span className="text-[12px] text-[var(--text-dim)]">a replay is already in progress</span>
                  )}
                  {!mayRun && <span className="text-[12px] text-[var(--text-dim)]">requires OPERATOR</span>}
                </div>
                {runMsg && (
                  <p className={`flex items-center gap-2 text-[13px] ${runMsg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>
                    <span>{runMsg.text}</span>
                    {runMsg.retryable && (
                      <Button onClick={() => void submitRun()} disabled={submitting}>
                        Retry
                      </Button>
                    )}
                  </p>
                )}
              </div>
            );
          }}
        </Await>
      </Section>

      <Section title="Runs">
        {/* WS-driven (replays topic): progress updates announce without
            stealing focus (§4.6/BL-23). */}
        <div aria-live="polite" aria-atomic="false">
        {runsState.kind === "loading" && <Loading what="replay runs" />}
        {runsState.kind === "error" && (
          <ErrorBox message={runsState.message} status={runsState.status} code={runsState.code} />
        )}
        {runsState.kind !== "loading" && runsState.kind !== "error" && (
          <Table
            head={["ID", "Recording", "Config", "Status", "Progress", "Started", "Finished", "Actor", ""]}
            empty="replay runs (run one above)"
            label="Replay runs"
              rowKeys={mergedRuns.map((run) => run.id)}
              rows={mergedRuns.map((run) => [
              <span key="id" className="text-[var(--text-dim)]">
                {run.id}
              </span>,
              run.recording,
              run.request.config_version ? `v${run.request.config_version}` : "live",
              <Badge key="s" tone={statusTone(run.status)}>
                {run.status}
              </Badge>,
              `${run.done}/${run.total}${run.step ? ` — ${run.step}` : ""}`,
              run.started_at ? fmtTime(run.started_at) : "—",
              run.finished_at ? fmtTime(run.finished_at) : "—",
              run.actor ?? "—",
              <Button
                key="v"
                onClick={() => viewRun(run.id)}
                disabled={detailID === run.id && detailState.kind === "loading"}
              >
                {detailID === run.id && detailState.kind === "loading" ? "loading…" : "View"}
              </Button>,
            ])}
          />
        )}
        </div>
      </Section>

      {detailID && (
        <Section title={`Run ${detailID} result`}>
          <Await state={detailState} what={`replay run ${detailID}`}>
            {(detail) =>
              detail.status === "failed" ? (
                <p className="text-[13px] text-[var(--critical)]">{detail.error || "Run failed."}</p>
              ) : (
                <>
                  <div className="mb-3 grid max-w-xl grid-cols-1 gap-3 sm:grid-cols-3">
                    <div className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3">
                      <div className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">Opportunities</div>
                      <div className="mt-1 text-sm font-medium">{detail.opportunities}</div>
                    </div>
                    <div className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3">
                      <div className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">Qualified</div>
                      <div className="mt-1 text-sm font-medium">{detail.qualified}</div>
                    </div>
                    <div className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3">
                      <div className="text-[11px] uppercase tracking-wider text-[var(--text-dim)]">Cycles</div>
                      <div className="mt-1 text-sm font-medium">{detail.cycles}</div>
                    </div>
                  </div>
                  <Table
                    head={["Opportunity", "Triangle", "Outcome", "Net bps", "At"]}
                    empty="executed cycles (top, ranked by net bps — not raw qualified opportunities)"
                    label="Top triangles in this run"
                rowKeys={(detail.top ?? []).map((t) => t.triangle_id)}
                rows={(detail.top ?? []).map((t) => [
                      <Link key="o" href={`/opportunities/${encodeURIComponent(t.opportunity_id)}`} className="text-[var(--accent)] underline">
                        {t.opportunity_id}
                      </Link>,
                      <Link key="t" href={`/triangles/${encodeURIComponent(t.triangle_id)}`} className="text-[var(--accent)] underline">
                        {t.triangle_id}
                      </Link>,
                      <OutcomeBadge key="o" code={t.outcome} />,
                      t.net_bps,
                      fmtTime(t.at),
                    ])}
                  />
                </>
              )
            }
          </Await>
        </Section>
      )}

      <Section title="Config comparison (any two versions)">
        <Await state={versions} what="config versions">
          {(list) => (
            <>
              <div className="mb-3 flex items-center gap-2 text-[13px]">
                {[
                  { label: "A", value: a, set: setA },
                  { label: "B", value: b, set: setB },
                ].map(({ label, value, set }) => (
                  <label key={label} className="flex items-center gap-1">
                    <span className="text-[var(--text-dim)]">{label}:</span>
                    <select
                      value={value}
                      onChange={(e) => set(e.target.value === "" ? "" : Number(e.target.value))}
                      className="rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                    >
                      <option value="">version…</option>
                      {(list ?? []).map((v) => (
                        <option key={v.version} value={v.version}>
                          v{v.version}
                          {v.active ? " (active)" : ""}
                        </option>
                      ))}
                    </select>
                  </label>
                ))}
                <Button onClick={compare} disabled={a === "" || b === ""}>
                  Compare
                </Button>
                {diffErr && <span className="text-[12px] text-[var(--critical)]">{diffErr}</span>}
              </div>
              {diff !== null &&
                (diff.length === 0 ? (
                  <p className="text-sm text-[var(--text-dim)]">Versions are identical.</p>
                ) : (
                  <Table
                    head={["Parameter", `v${a}`, `v${b}`]}
                    empty="differences"
                    label="Configuration differences"
                rowKeys={diff.map((d) => d.path)}
                rows={diff.map((d) => [d.path, d.a, d.b])}
                  />
                ))}
            </>
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
