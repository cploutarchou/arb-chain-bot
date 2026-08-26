"use client";

import { useState } from "react";
import { api, ApiError, type StrategyParams } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, PageTitle, Section, Table, fmtTime } from "@/components/ui";

export default function StrategiesPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.config.current(), 10000, [refresh]);
  const versions = usePoll(() => api.config.versions(25), 10000, [refresh]);
  const [draft, setDraft] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const apply = async () => {
    if (draft === null) return;
    setMsg(null);
    let parsed: StrategyParams;
    try {
      parsed = JSON.parse(draft) as StrategyParams;
    } catch {
      setMsg({ ok: false, text: "Draft is not valid JSON." });
      return;
    }
    try {
      const snap = await api.config.apply(parsed);
      setMsg({ ok: true, text: `Version ${snap.version} active.` });
      setDraft(null);
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Apply failed.",
      });
    }
  };

  const rollback = async (version: number) => {
    setMsg(null);
    try {
      const snap = await api.config.rollback(version);
      setMsg({ ok: true, text: `Rolled back as new version ${snap.version}.` });
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Rollback failed." });
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
                    <Button onClick={apply}>Apply as new version</Button>
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
                v.diff ? Object.keys(v.diff).join(", ") || "—" : "—",
                !v.active && mayEdit ? (
                  <Button key="rb" onClick={() => rollback(v.version)}>
                    Roll back to
                  </Button>
                ) : (
                  ""
                ),
              ])}
            />
          )}
        </Await>
      </Section>
    </ConsoleShell>
  );
}
