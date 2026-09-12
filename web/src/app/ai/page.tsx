"use client";

import { useState } from "react";
import { api, ApiError, type AIRecommendation } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import { Await, Badge, Button, ConfirmDialog, PageTitle, Section, Table, fmtTime } from "@/components/ui";

export default function AIPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const [refresh, setRefresh] = useState(0);
  const analyses = usePoll(() => api.ai.analyses(5), 15000, [refresh]);
  const recs = usePoll(() => api.ai.recommendations(""), 10000, [refresh]);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [approveConfirm, setApproveConfirm] = useState<AIRecommendation | null>(null);
  const mayDecide = can(role, "ai:approve");

  const decide = async (fn: () => Promise<unknown>, verb: string) => {
    setMsg(null);
    try {
      await fn();
      setMsg({ ok: true, text: `Recommendation ${verb}.` });
    } catch (err: unknown) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : `${verb} failed` });
    } finally {
      setRefresh((n) => n + 1);
    }
  };

  return (
    <ConsoleShell>
      <PageTitle>AI Advisor</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        The advisor is an analyst: recommendations change nothing until a human approves them, and
        approvals run through the same validated, versioned, audited config path as a manual edit.
        The deterministic risk engine is never overridden.
      </p>
      {msg && (
        <p className={`mb-3 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>
      )}
      <Section title="Recommendations">
        <Await state={recs} what="recommendations">
          {(list) => (
            <Table
              head={["Created", "Parameter", "Current → Recommended", "Confidence", "Reason", "Status", ""]}
              empty="recommendations (the advisor proposes only when data supports it)"
              label="AI recommendations"
            rowKeys={(list ?? []).map((r) => `${r.parameter}:${r.created_at}`)}
            rows={(list ?? []).map((r) => [
                fmtTime(r.created_at),
                r.parameter,
                `${r.current_value} → ${r.recommended_value}`,
                r.confidence,
                <span key="why" className="max-w-sm truncate" title={`${r.reason}\nEvidence: ${r.evidence}\nRisks: ${r.risks}`}>
                  {r.reason}
                </span>,
                <Badge
                  key="st"
                  tone={r.status === "proposed" ? "warn" : r.status === "approved" ? "ok" : "dim"}
                >
                  {r.status}
                </Badge>,
                r.status === "proposed" && mayDecide ? (
                  <span key="act" className="flex gap-1">
                    <Button onClick={() => setApproveConfirm(r)}>Approve</Button>
                    <Button onClick={() => decide(() => api.ai.reject(r.id), "rejected")} danger>
                      Reject
                    </Button>
                  </span>
                ) : (
                  r.decided_by || ""
                ),
              ])}
            />
          )}
        </Await>
      </Section>
      <Section title="Recent analyses">
        <Await state={analyses} what="analyses">
          {(list) => (
            <div className="space-y-3">
              {(list ?? []).length === 0 && (
                <p className="text-sm text-[var(--text-dim)]">
                  No analyses yet (scheduler runs hourly/daily/weekly when a provider is configured).
                </p>
              )}
              {(list ?? []).map((a) => (
                <div key={a.id} className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3">
                  <div className="mb-1 flex items-center gap-2 text-[12px] text-[var(--text-dim)]">
                    <Badge tone="dim">{a.kind}</Badge>
                    <span>{fmtTime(a.at)}</span>
                    <span>model {a.model}</span>
                    <span>config v{a.config_version}</span>
                  </div>
                  <p className="text-[13px]">{a.summary}</p>
                  {a.findings?.length > 0 && (
                    <ul className="mt-1 list-inside list-disc text-[12px] text-[var(--text-dim)]">
                      {a.findings.map((f, i) => (
                        <li key={i}>{f}</li>
                      ))}
                    </ul>
                  )}
                </div>
              ))}
            </div>
          )}
        </Await>
      </Section>

      {approveConfirm && (
        <ConfirmDialog
          title={`Approve recommendation: ${approveConfirm.parameter} ${approveConfirm.current_value} → ${approveConfirm.recommended_value}?`}
          confirmLabel="Approve"
          onCancel={() => setApproveConfirm(null)}
          onConfirm={() => {
            const rec = approveConfirm;
            setApproveConfirm(null);
            void decide(() => api.ai.approve(rec.id), "approved");
          }}
          body={
            <>
              <p className="mb-3">
                Reason: <em>{approveConfirm.reason}</em>. This applies immediately as a new config
                version, auditable exactly like a manual change.
              </p>
              <Button
                danger
                onClick={() => {
                  const rec = approveConfirm;
                  setApproveConfirm(null);
                  void decide(() => api.ai.reject(rec.id), "rejected");
                }}
              >
                Reject instead
              </Button>
            </>
          }
        />
      )}
    </ConsoleShell>
  );
}
