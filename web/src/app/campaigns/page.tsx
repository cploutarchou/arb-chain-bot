"use client";

// Recording control + §80 profitability campaigns. Recordings capture
// public Binance market-data depth only — no orders are ever placed —
// and campaigns replay that recorded data offline against the recorded
// fee schedule; nothing here trades live.

import { useEffect, useRef, useState } from "react";
import { api, ApiError, isNotReady, type CampaignRun, type RecorderStatus } from "@/lib/api/client";
import { fmtBytes, fmtDurationMs } from "@/lib/format";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { connectHub, type HubMessage } from "@/lib/ws";
import { flagsTone, worstVerdict, BAD_PHRASES } from "@/lib/campaignVerdict";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, ErrorBox, Loading, PageTitle, Section, Stat, Table, fmtTime } from "@/components/ui";

interface RecordingsTopicMsg {
  kind?: "recorder";
  recorder?: RecorderStatus;
}

interface CampaignsTopicMsg {
  kind?: "campaign_run";
  run?: CampaignRun;
  runs?: CampaignRun[];
}

function statusTone(status: CampaignRun["status"]): "ok" | "warn" | "bad" | "dim" {
  if (status === "done") return "ok";
  if (status === "running") return "warn";
  if (status === "failed") return "bad";
  return "dim"; // queued
}

// Overlay live WS runs onto the polled list: polled rows take the live
// version when present, and any run that only exists on the socket (just
// POSTed, not yet in a poll response) is prepended.
function mergeRuns(polled: CampaignRun[], live: Record<string, CampaignRun>): CampaignRun[] {
  const seen = new Set(polled.map((r) => r.id));
  const merged = polled.map((r) => live[r.id] ?? r);
  const extra = Object.values(live).filter((r) => !seen.has(r.id));
  return [...extra, ...merged];
}

type RunDetail =
  | { kind: "loading"; id: string }
  | { kind: "error"; id: string; message: string }
  | { kind: "ready"; id: string; run: CampaignRun };

export default function CampaignsPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayControlRecorder = can(role, "recordings:control");
  const mayRunCampaign = can(role, "campaigns:run");

  const [refresh, setRefresh] = useState(0);
  const recordingsState = usePoll(() => api.recordings.list(), 5000, [refresh]);
  const runsState = usePoll(() => api.campaigns.list(50), 5000, [refresh]);
  // Restart status is shown on the Recorder card (T-057 design §4: "the
  // /campaigns Recorder card should show when a restart is pending/in
  // progress") since a pending/in-progress restart is exactly the state
  // where recorder start/stop can behave surprisingly (a restart may stop
  // the active recording itself).
  const engineStatus = usePoll(() => api.engine.status(), 10000);

  const [wsStatus, setWsStatus] = useState<"connecting" | "open" | "closed">("connecting");
  const [wsRecorder, setWsRecorder] = useState<RecorderStatus | null>(null);
  const [wsRuns, setWsRuns] = useState<Record<string, CampaignRun>>({});

  const [recorderErr, setRecorderErr] = useState("");
  // retryable marks the 503 not_ready a campaign launch can return
  // briefly after boot (the runner's background context isn't wired
  // yet) — the message is shown verbatim, with a Retry action rather
  // than a reworded hint.
  const [runMsg, setRunMsg] = useState<{ ok: boolean; text: string; retryable?: boolean } | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [detail, setDetail] = useState<RunDetail | null>(null);

  const formRef = useRef<HTMLDivElement>(null);

  const [selRecording, setSelRecording] = useState("");
  const [assetsText, setAssetsText] = useState("USDT");
  const [balanceText, setBalanceText] = useState("USDT=10000");
  const [seedsText, setSeedsText] = useState("1,2,3");
  const [makerBps, setMakerBps] = useState("10");
  const [takerBps, setTakerBps] = useState("10");
  const [grid, setGrid] = useState<"full" | "baseline">("full");

  useEffect(() => {
    return connectHub(["recordings", "campaigns"], {
      onStatus: (s) => {
        setWsStatus(s);
        if (s === "closed") {
          // Socket down: fall back to the 5s poll rather than showing a
          // frozen "live" value that can never be overwritten again.
          setWsRecorder(null);
          setWsRuns((prev) => (Object.keys(prev).length ? {} : prev));
        }
      },
      onMessage: (msg: HubMessage) => {
        if (msg.topic === "recordings") {
          const data = msg.data as RecordingsTopicMsg;
          if (data.recorder) setWsRecorder(data.recorder);
          return;
        }
        if (msg.topic === "campaigns") {
          const data = msg.data as CampaignsTopicMsg;
          if (msg.snapshot) {
            const runs = data.runs ?? [];
            setWsRuns((prev) => {
              const next = { ...prev };
              for (const run of runs) next[run.id] = run;
              return next;
            });
            return;
          }
          if (data.kind === "campaign_run" && data.run) {
            const run = data.run;
            setWsRuns((prev) => ({ ...prev, [run.id]: run }));
          }
        }
      },
    });
  }, []);

  const polledRuns = runsState.kind === "ready" ? (runsState.data.runs ?? []) : [];
  const mergedRuns = mergeRuns(polledRuns, wsRuns);
  const runInProgress = mergedRuns.some((r) => r.status === "queued" || r.status === "running");

  const controlRecorder = async (fn: () => Promise<{ session_id: string; recorder: RecorderStatus }>) => {
    setRecorderErr("");
    try {
      const res = await fn();
      setWsRecorder(res.recorder);
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setRecorderErr(err instanceof ApiError ? err.message : "Recorder control failed.");
    }
  };

  const prefillRun = (id: string) => {
    setSelRecording(id);
    formRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const submitRun = async () => {
    setRunMsg(null);
    if (!selRecording) {
      setRunMsg({ ok: false, text: "Select a recording." });
      return;
    }
    const assets = assetsText
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    const balances: Record<string, string> = {};
    for (const pair of balanceText.split(",")) {
      const [asset, amount] = pair.split("=").map((s) => s.trim());
      if (asset && amount) balances[asset] = amount;
    }
    const seeds = seedsText
      .split(",")
      .map((s) => Number(s.trim()))
      .filter((n) => Number.isFinite(n));
    setSubmitting(true);
    try {
      const res = await api.campaigns.run({
        recording: selRecording,
        assets: assets.length ? assets : undefined,
        balances: Object.keys(balances).length ? balances : undefined,
        seeds: seeds.length ? seeds : undefined,
        fee_maker_bps: makerBps.trim() ? Number(makerBps) : undefined,
        fee_taker_bps: takerBps.trim() ? Number(takerBps) : undefined,
        grid,
      });
      setRunMsg({ ok: true, text: `Run ${res.run.id} queued.` });
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setRunMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Campaign launch failed.",
        retryable: isNotReady(err),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const viewRun = async (id: string) => {
    setDetail({ kind: "loading", id });
    try {
      const res = await api.campaigns.get(id);
      setDetail({ kind: "ready", id, run: res.run });
    } catch (err: unknown) {
      setDetail({
        kind: "error",
        id,
        message: err instanceof ApiError ? err.message : "Failed to load run.",
      });
    }
  };

  return (
    <ConsoleShell active="Campaigns">
      <PageTitle>Campaigns</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Recordings capture public Binance depth only — no orders are ever placed. The §80 campaign
        replays a closed recording offline against a fee/seed grid to verify profitability before any
        capital is risked; it never trades live. (WS {wsStatus === "open" ? "connected" : wsStatus})
      </p>

      <Section title="Recorder">
        {engineStatus.kind === "ready" &&
          (engineStatus.data.restart.state === "pending" || engineStatus.data.restart.state === "restarting") && (
            <p className="mb-2 rounded border border-[var(--warn)] px-2 py-1.5 text-[12px] text-[var(--warn)]">
              {engineStatus.data.restart.state === "restarting"
                ? "Engine restart in progress — recorder state may change once it completes."
                : `Engine restart pending: ${engineStatus.data.restart.pending_reasons?.join("; ") ?? "settings changed"}.`}
            </p>
          )}
        {/* WS-driven (recordings topic): announce state changes to screen
            readers without interrupting (§4.6/BL-23). */}
        <div aria-live="polite" aria-atomic="false">
        <Await state={recordingsState} what="recorder status">
          {(r) => {
            const recorder = wsRecorder ?? r.recorder;
            if (!recorder) {
              return (
                <p className="text-sm text-[var(--text-dim)]">
                  Recorder not available (requires an engine profile with market-data ingestion).
                </p>
              );
            }
            const uptime = recorder.started_at
              ? fmtDurationMs(Date.now() - new Date(recorder.started_at).getTime())
              : "—";
            return (
              <div className="max-w-4xl">
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-4">
                  <Stat
                    label="State"
                    value={recorder.running ? "RECORDING" : "IDLE"}
                    tone={recorder.running ? "ok" : "warn"}
                  />
                  <Stat label="Session" value={recorder.session_id ?? "—"} />
                  <Stat label="Uptime" value={uptime} />
                  <Stat label="Frames written" value={recorder.frames_written} />
                  <Stat
                    label="Frames dropped"
                    value={recorder.frames_dropped}
                    tone={recorder.frames_dropped > 0 ? "bad" : undefined}
                  />
                  <Stat label="Segments closed" value={recorder.segments_closed} />
                  <Stat label="Bytes closed" value={fmtBytes(recorder.bytes_closed)} />
                </div>
                <div className="mt-2 text-[12px] text-[var(--text-dim)]">
                  Symbols: {(recorder.symbols ?? []).join(", ") || "—"}
                </div>
                <div className="mt-3 flex items-center gap-2">
                  {!recorder.running ? (
                    <Button
                      onClick={() => controlRecorder(api.recordings.start)}
                      disabled={!mayControlRecorder}
                    >
                      Start recording
                    </Button>
                  ) : (
                    <Button
                      onClick={() => controlRecorder(api.recordings.stop)}
                      disabled={!mayControlRecorder}
                      danger
                    >
                      Stop recording
                    </Button>
                  )}
                  {!mayControlRecorder && (
                    <span className="text-[12px] text-[var(--text-dim)]">controls require OPERATOR</span>
                  )}
                </div>
                {recorderErr && <p className="mt-2 text-[12px] text-[var(--critical)]">{recorderErr}</p>}
              </div>
            );
          }}
        </Await>
        </div>
      </Section>

      <Section title="Recorded sessions">
        <Await state={recordingsState} what="recordings">
          {(r) => (
            <Table
              head={["ID", "Started", "Ended", "Segments", "Exchange", ""]}
              empty="recordings (start recording above to capture market data)"
              rows={(r.recordings ?? []).map((rec) => [
                <span key="id" className="text-[var(--text-dim)]">
                  {rec.id}
                </span>,
                fmtTime(rec.started_at),
                rec.ended_at ? fmtTime(rec.ended_at) : <Badge tone="warn">open</Badge>,
                (rec.segment_files ?? []).length,
                rec.exchange_id,
                rec.ended_at ? (
                  <Button
                    key="run"
                    onClick={() => prefillRun(rec.id)}
                    disabled={!mayRunCampaign || runInProgress}
                  >
                    Configure campaign…
                  </Button>
                ) : (
                  ""
                ),
              ])}
            />
          )}
        </Await>
      </Section>

      <Section title="Run §80 campaign">
        <Await state={recordingsState} what="recordings">
          {(r) => (
            <div ref={formRef} className="max-w-2xl space-y-3 text-[13px]">
              <label className="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-2">
                <span className="sm:w-40 sm:shrink-0 text-[var(--text-dim)]">Recording</span>
                <select
                  value={selRecording}
                  onChange={(e) => setSelRecording(e.target.value)}
                  className="flex-1 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                >
                  <option value="">select a recording…</option>
                  {(r.recordings ?? []).map((rec) => (
                    <option key={rec.id} value={rec.id}>
                      {rec.id} ({rec.exchange_id})
                    </option>
                  ))}
                </select>
              </label>
              <label className="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-2">
                <span className="sm:w-40 sm:shrink-0 text-[var(--text-dim)]">Starting assets</span>
                <input
                  value={assetsText}
                  onChange={(e) => setAssetsText(e.target.value)}
                  className="flex-1 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                  placeholder="USDT"
                />
              </label>
              <label className="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-2">
                <span className="sm:w-40 sm:shrink-0 text-[var(--text-dim)]">Balances (ASSET=amount,…)</span>
                <input
                  value={balanceText}
                  onChange={(e) => setBalanceText(e.target.value)}
                  className="flex-1 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                  placeholder="USDT=10000"
                />
              </label>
              <label className="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-2">
                <span className="sm:w-40 sm:shrink-0 text-[var(--text-dim)]">Seeds</span>
                <input
                  value={seedsText}
                  onChange={(e) => setSeedsText(e.target.value)}
                  className="flex-1 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                  placeholder="1,2,3"
                />
              </label>
              <label className="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-2">
                <span className="sm:w-40 sm:shrink-0 text-[var(--text-dim)]">Maker fee (bps)</span>
                <input
                  value={makerBps}
                  onChange={(e) => setMakerBps(e.target.value)}
                  type="number"
                  className="w-32 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                />
              </label>
              <label className="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-2">
                <span className="sm:w-40 sm:shrink-0 text-[var(--text-dim)]">Taker fee (bps)</span>
                <input
                  value={takerBps}
                  onChange={(e) => setTakerBps(e.target.value)}
                  type="number"
                  className="w-32 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                />
              </label>
              <label className="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-2">
                <span className="sm:w-40 sm:shrink-0 text-[var(--text-dim)]">Grid</span>
                <select
                  value={grid}
                  onChange={(e) => setGrid(e.target.value as "full" | "baseline")}
                  className="rounded border border-[var(--border)] bg-[var(--bg-panel)] px-2 py-1 outline-none"
                >
                  <option value="full">full</option>
                  <option value="baseline">baseline</option>
                </select>
              </label>
              <div className="flex items-center gap-2">
                <Button
                  onClick={submitRun}
                  disabled={!mayRunCampaign || submitting || runInProgress || !selRecording}
                >
                  {submitting ? "Launching…" : "Launch campaign"}
                </Button>
                {runInProgress && (
                  <span className="text-[12px] text-[var(--text-dim)]">a run is already in progress</span>
                )}
                {!mayRunCampaign && (
                  <span className="text-[12px] text-[var(--text-dim)]">requires OPERATOR</span>
                )}
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
          )}
        </Await>
      </Section>

      <Section title="Runs">
        {/* WS-driven (campaigns topic): progress/status updates announce
            without stealing focus (§4.6/BL-23). */}
        <div aria-live="polite" aria-atomic="false">
        {runsState.kind === "loading" && <Loading what="campaign runs" />}
        {runsState.kind === "error" && (
          <ErrorBox message={runsState.message} status={runsState.status} />
        )}
        {runsState.kind !== "loading" && runsState.kind !== "error" && (
          <Table
            head={["ID", "Recording", "Status", "Progress", "Verdict", "Started", "Finished", "Actor", ""]}
            empty="campaign runs (launch one above)"
            rows={mergedRuns.map((run) => {
              const v = worstVerdict(run);
              return [
                <span key="id" className="text-[var(--text-dim)]">
                  {run.id}
                </span>,
                run.recording,
                <Badge key="s" tone={statusTone(run.status)}>
                  {run.status}
                </Badge>,
                `${run.done}/${run.total}${run.step ? ` — ${run.step}` : ""}`,
                // Verdict cell wraps within its column; never truncated,
                // never behind a title= tooltip (§3.2/§4.5 — a hidden
                // verdict is functionally a hidden verdict).
                v ? (
                  <div key="v" className="max-w-xs whitespace-normal break-words">
                    <span className={v.tone === "bad" ? "text-[var(--critical)]" : v.tone === "high" ? "text-[var(--high)]" : v.tone === "warn" ? "text-[var(--warn)]" : v.tone === "ok" ? "text-[var(--ok)]" : "text-[var(--text-dim)]"}>
                      {v.text}
                    </span>
                  </div>
                ) : (
                  <span key="v" className="text-[var(--text-dim)]">
                    — pending —
                  </span>
                ),
                run.started_at ? fmtTime(run.started_at) : "—",
                run.finished_at ? fmtTime(run.finished_at) : "—",
                run.actor ?? "—",
                <Button key="v-btn" onClick={() => viewRun(run.id)}>
                  View
                </Button>,
              ];
            })}
          />
        )}
        </div>
      </Section>

      {detail && (
        <Section title={`Run ${detail.id}`}>
          {detail.kind === "loading" && <Loading what="run detail" />}
          {detail.kind === "error" && <ErrorBox message={detail.message} />}
          {detail.kind === "ready" && (
            <>
              {detail.run.flags && Object.keys(detail.run.flags).length > 0 && (
                <div
                  className={`mb-3 rounded border p-3 text-[13px] ${
                    flagsTone(detail.run.flags) === "bad"
                      ? "border-[var(--critical)]"
                      : flagsTone(detail.run.flags) === "ok"
                        ? "border-[var(--ok)]"
                        : "border-[var(--border)]"
                  }`}
                >
                  <div className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
                    Verdict
                  </div>
                  {Object.entries(detail.run.flags).map(([key, values]) => (
                    <div key={key} className="mb-2">
                      <div className="text-[12px] text-[var(--text-dim)]">{key}</div>
                      {values.map((v, i) => (
                        <div
                          key={i}
                          className={
                            BAD_PHRASES.some((p) => v.includes(p))
                              ? "text-[var(--critical)]"
                              : "text-[var(--text)]"
                          }
                        >
                          {v}
                        </div>
                      ))}
                    </div>
                  ))}
                </div>
              )}
              {detail.run.report_md ? (
                <pre className="max-h-[32rem] overflow-auto whitespace-pre-wrap rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3 text-[12px]">
                  {detail.run.report_md}
                </pre>
              ) : (
                <p className="text-sm text-[var(--text-dim)]">No report generated yet.</p>
              )}
              <div className="mt-2 space-y-1 text-[12px] text-[var(--text-dim)]">
                {detail.run.report_path && (
                  <div>
                    Report:{" "}
                    <code className="rounded bg-[var(--bg-panel)] px-1.5 py-0.5">
                      {detail.run.report_path}
                    </code>
                  </div>
                )}
                {detail.run.json_path && (
                  <div>
                    JSON:{" "}
                    <code className="rounded bg-[var(--bg-panel)] px-1.5 py-0.5">
                      {detail.run.json_path}
                    </code>
                  </div>
                )}
              </div>
            </>
          )}
        </Section>
      )}
    </ConsoleShell>
  );
}
