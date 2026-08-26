"use client";

import { useState } from "react";
import Link from "next/link";
import { api, request } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { flatten } from "@/lib/diff";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, PageTitle, Section, Table, fmtTime } from "@/components/ui";

interface RecordingRow {
  id: string;
  exchange_id: string;
  started_at: string;
  ended_at?: string;
  streams: Record<string, string>;
  segment_files: { path: string; from_ts: string; to_ts: string; frames: number; bytes: number; sha256: string }[];
}

interface ConfigSnapshotFull {
  version: number;
  params: Record<string, unknown>;
}

export default function ReplayPage() {
  const recordings = usePoll(
    () => request<{ recordings: RecordingRow[] | null }>("/api/v1/recordings"),
    15000,
  );
  const versions = usePoll(() => api.config.versions(50), 15000);
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
    <ConsoleShell active="Replay & Backtesting">
      <PageTitle>Replay &amp; Backtesting</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Recordings capture raw WS frames and REST snapshots; replay drives the exact same decode →
        validate → apply → evaluate path as live, and the same recording + config + seed produces an
        identical decision log (fingerprint-tested). Runs start from the CLI today; in-console runs
        arrive with the backtest worker.
      </p>
      <Section title="Recorded sessions">
        <Await state={recordings} what="recordings">
          {(r) =>
            (r.recordings ?? []).length === 0 ? (
              <p className="text-sm text-[var(--text-dim)]">
                No recordings yet. Start one from <Link href="/campaigns" className="text-[var(--accent)]">Campaigns → Recorder</Link>.
              </p>
            ) : (
              <Table
                head={["Started", "Ended", "Exchange", "Session", "Segments", "Frames", "Replay (CLI only for now)"]}
                empty="recordings"
                rows={(r.recordings ?? []).map((rec) => {
                  const frames = (rec.segment_files ?? []).reduce((n, s) => n + (s.frames ?? 0), 0);
                  return [
                    fmtTime(rec.started_at),
                    rec.ended_at ? fmtTime(rec.ended_at) : <Badge tone="warn">open</Badge>,
                    rec.exchange_id,
                    <span key="id" className="text-[var(--text-dim)]">{rec.id}</span>,
                    (rec.segment_files ?? []).length,
                    frames,
                    <code key="cmd" className="rounded bg-[var(--bg-panel)] px-1.5 py-0.5 text-[11px]">
                      ARB_MODE=REPLAY ARB_REPLAY_SESSION={rec.id} ./arbd
                    </code>,
                  ];
                })}
              />
            )
          }
        </Await>
      </Section>
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
