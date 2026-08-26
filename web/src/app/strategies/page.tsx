"use client";

import { useState } from "react";
import { api, ApiError, type StrategyParams } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { diffParams, effectFor, fmtDiffValue, type DiffRow } from "@/lib/diff";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, ConfirmDialog, DiffTable, PageTitle, Section, Table, fmtTime } from "@/components/ui";

interface ApplyConfirm {
  params: StrategyParams;
  rows: (DiffRow & { effect: string })[];
  fromVersion: number;
}

interface RollbackConfirm {
  version: number;
  rows: DiffRow[];
  fromVersion: number;
}

export default function StrategiesPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.config.current(), 10000, [refresh]);
  const versions = usePoll(() => api.config.versions(25), 10000, [refresh]);
  const [draft, setDraft] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [applyConfirm, setApplyConfirm] = useState<ApplyConfirm | null>(null);
  const [rollbackConfirm, setRollbackConfirm] = useState<RollbackConfirm | null>(null);
  const [rollbackLoading, setRollbackLoading] = useState<number | null>(null);
  const [rollbackErr, setRollbackErr] = useState("");

  const activeParams = current.kind === "ready" ? current.data.params : null;
  const activeVersion = current.kind === "ready" ? current.data.version : null;

  const reviewChanges = () => {
    if (draft === null || activeParams === null || activeVersion === null) return;
    setMsg(null);
    let parsed: StrategyParams;
    try {
      parsed = JSON.parse(draft) as StrategyParams;
    } catch {
      setMsg({ ok: false, text: "Draft is not valid JSON." });
      return;
    }
    const rows = diffParams(
      activeParams as unknown as Record<string, unknown>,
      parsed as unknown as Record<string, unknown>,
    ).map((r) => ({ ...r, effect: effectFor(r.path) }));
    setApplyConfirm({ params: parsed, rows, fromVersion: activeVersion });
  };

  const confirmApply = async () => {
    if (!applyConfirm) return;
    setMsg(null);
    try {
      const snap = await api.config.apply(applyConfirm.params);
      setMsg({ ok: true, text: `Version ${snap.version} active.` });
      setDraft(null);
      setApplyConfirm(null);
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Apply failed.",
      });
      setApplyConfirm(null);
    }
  };

  const reviewRollback = async (version: number) => {
    if (activeParams === null || activeVersion === null) return;
    setRollbackErr("");
    setRollbackLoading(version);
    try {
      const target = await api.config.version(version);
      const rows = diffParams(
        activeParams as unknown as Record<string, unknown>,
        target.params as unknown as Record<string, unknown>,
      );
      setRollbackConfirm({ version, rows, fromVersion: activeVersion });
    } catch (err: unknown) {
      setRollbackErr(err instanceof ApiError ? err.message : "Could not load that version.");
    } finally {
      setRollbackLoading(null);
    }
  };

  const confirmRollback = async () => {
    if (!rollbackConfirm) return;
    setMsg(null);
    try {
      const snap = await api.config.rollback(rollbackConfirm.version);
      setMsg({ ok: true, text: `Rolled back as new version ${snap.version}.` });
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Rollback failed." });
    } finally {
      setRollbackConfirm(null);
    }
  };

  const mayEdit = can(role, "scanner:config");

  return (
    <ConsoleShell active="Strategies">
      <PageTitle>Strategy Configuration</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Every change becomes an immutable version with a diff, actor, and audit event, and hot-swaps
        into the running scanner. Risk-section changes require ADMIN; the backend validates bounds.
      </p>
      {msg && (
        <p className={`mb-3 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>
      )}
      <Section title="Active version">
        <Await state={current} what="active config">
          {(c) => (
            <>
              <div className="mb-2 flex items-center gap-3 text-[13px]">
                <Badge tone="ok">v{c.version}</Badge>
                <span className="text-[var(--text-dim)]">
                  created {fmtTime(c.created_at)} by {c.created_by || "system"}
                  {c.parent_version ? ` (parent v${c.parent_version})` : ""}
                </span>
                {mayEdit && draft === null && (
                  <Button onClick={() => setDraft(JSON.stringify(c.params, null, 2))}>Edit draft</Button>
                )}
              </div>
              {draft === null ? (
                <pre className="max-h-96 overflow-auto rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3 text-[12px]">
                  {JSON.stringify(c.params, null, 2)}
                </pre>
              ) : (
                <>
                  <textarea
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    spellCheck={false}
                    className="h-96 w-full rounded border border-[var(--accent)] bg-[var(--bg-panel)] p-3 font-mono text-[12px] outline-none"
                  />
                  <div className="mt-2 flex gap-2">
                    <Button onClick={reviewChanges}>Apply as new version</Button>
                    <Button onClick={() => setDraft(null)} danger>
                      Discard draft
                    </Button>
                  </div>
                </>
              )}
            </>
          )}
        </Await>
      </Section>
      <Section title="Version history (rollback creates a new version)">
        {rollbackErr && <p className="mb-2 text-[12px] text-[var(--critical)]">{rollbackErr}</p>}
        <Await state={versions} what="config versions">
          {(list) => (
            <Table
              head={["Version", "Created", "By", "Parent", "Changed paths", ""]}
              empty="versions"
              rows={(list ?? []).map((v) => [
                v.active ? <Badge key="a" tone="ok">v{v.version} active</Badge> : `v${v.version}`,
                fmtTime(v.created_at),
                v.created_by || "system",
                v.parent_version ? `v${v.parent_version}` : "—",
                v.diff && Object.keys(v.diff).length > 0 ? (
                  <div key="d" className="max-w-xs whitespace-normal break-words text-[12px]">
                    {Object.entries(v.diff).map(([path, change]) => (
                      <div key={path}>
                        <span className="text-[var(--text-dim)]">{path}:</span>{" "}
                        {fmtDiffValue(change.old)} → {fmtDiffValue(change.new)}
                      </div>
                    ))}
                  </div>
                ) : (
                  "—"
                ),
                !v.active && mayEdit ? (
                  <Button
                    key="rb"
                    onClick={() => reviewRollback(v.version)}
                    disabled={rollbackLoading === v.version}
                  >
                    {rollbackLoading === v.version ? "Loading…" : "Roll back to"}
                  </Button>
                ) : (
                  ""
                ),
              ])}
            />
          )}
        </Await>
      </Section>

      {applyConfirm && (
        <ConfirmDialog
          title="Apply new strategy configuration?"
          confirmLabel="Apply new version"
          onConfirm={confirmApply}
          onCancel={() => setApplyConfirm(null)}
          body={
            <>
              <p className="mb-3">
                This becomes a new version on top of v{applyConfirm.fromVersion}. Fields marked{" "}
                <strong>immediate</strong> take effect in the running scanner as soon as you confirm;
                fields marked <strong>on restart</strong> are saved now but only take effect the next
                time the engine process starts.
              </p>
              <DiffTable
                rows={applyConfirm.rows}
                beforeLabel={`Current (v${applyConfirm.fromVersion})`}
                afterLabel="New (draft)"
                showEffect
              />
            </>
          }
        />
      )}

      {rollbackConfirm && (
        <ConfirmDialog
          title={`Roll back to v${rollbackConfirm.version}?`}
          confirmLabel={`Roll back to v${rollbackConfirm.version}`}
          onConfirm={confirmRollback}
          onCancel={() => setRollbackConfirm(null)}
          body={
            <>
              <p className="mb-3">
                This creates a new version with v{rollbackConfirm.version}&apos;s parameters. Versions
                between v{rollbackConfirm.version} and v{rollbackConfirm.fromVersion} remain in history
                and can be rolled back to again later.
              </p>
              <DiffTable
                rows={rollbackConfirm.rows}
                beforeLabel={`Current (v${rollbackConfirm.fromVersion})`}
                afterLabel={`Restoring (v${rollbackConfirm.version})`}
              />
            </>
          }
        />
      )}
    </ConsoleShell>
  );
}
