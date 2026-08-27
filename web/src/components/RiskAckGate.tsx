"use client";

// RiskAckGate: a blocking full-page acknowledgement, rendered instead of
// the console whenever the authenticated organisation has not accepted
// the current risk-disclosure version (billing.md §1.4, compliance
// review #3/#11). Every protected backend route 403s risk_ack_required
// until this happens; /me, /me/risk-ack, logout and own-password stay
// reachable — this component only ever needs those.

import { useState } from "react";
import { api, ApiError } from "@/lib/api/client";
import { useAuth } from "@/lib/auth";
import { Button } from "@/components/ui";
import {
  RISK_ACK_CONFIRM_HELP,
  RISK_ACK_CONFIRM_LABEL,
  RISK_ACK_CTA,
  RISK_ACK_DECLINED_BODY,
  RISK_ACK_DECLINE_LABEL,
  RISK_DISCLOSURE_SUMMARY,
  RISK_DISCLOSURE_TITLE,
  RISK_DISCLOSURE_VERSION,
} from "@/content/riskDisclosure";

export function RiskAckGate({ children }: { children: React.ReactNode }) {
  const { state, refreshMe, logout } = useAuth();
  const [checked, setChecked] = useState(false);
  const [declined, setDeclined] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  if (state.kind !== "authenticated" || !state.me.risk_ack_required) {
    return <>{children}</>;
  }

  // Always the version the backend is actually asking for, never a
  // hardcoded constant — the backend 409s a mismatched version
  // (handleRiskAck) and the constant is only a display/initial fallback.
  const version = state.me.risk_ack_version || RISK_DISCLOSURE_VERSION;

  const accept = async () => {
    setBusy(true);
    setErr("");
    try {
      await api.riskAck.accept(version);
      await refreshMe();
    } catch (e: unknown) {
      setErr(
        e instanceof ApiError
          ? e.message
          : "Could not record the acknowledgement. Try again.",
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-[var(--overlay)] p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="risk-ack-title"
        className="max-h-[90vh] w-full max-w-2xl overflow-y-auto rounded border border-[var(--border-strong)] bg-[var(--bg-panel)] p-6 shadow-lg"
      >
        <h1
          id="risk-ack-title"
          className="mb-1 text-lg font-semibold text-[var(--text)]"
        >
          {RISK_DISCLOSURE_TITLE}
        </h1>
        <p className="mb-4 text-[12px] text-[var(--text-dim)]">
          Risk Disclosure version {version}
        </p>

        <div className="mb-4 space-y-3 rounded border border-[var(--border)] bg-[var(--bg)] p-3 text-[13px] leading-relaxed text-[var(--text)]">
          {RISK_DISCLOSURE_SUMMARY.map((para, i) => (
            <p key={i}>{para}</p>
          ))}
        </div>

        {declined ? (
          <div className="mb-4 rounded border border-[var(--warn)] p-3 text-[13px] text-[var(--warn)]">
            {RISK_ACK_DECLINED_BODY}
          </div>
        ) : (
          <label className="mb-1 flex items-start gap-2 text-[13px] text-[var(--text)]">
            <input
              type="checkbox"
              checked={checked}
              onChange={(e) => setChecked(e.target.checked)}
              className="mt-0.5"
            />
            <span>{RISK_ACK_CONFIRM_LABEL}</span>
          </label>
        )}
        <p className="mb-4 text-[12px] text-[var(--text-dim)]">
          {RISK_ACK_CONFIRM_HELP}
        </p>

        {err && (
          <p className="mb-3 text-[13px] text-[var(--critical)]">{err}</p>
        )}

        <div className="flex flex-wrap items-center gap-2">
          <Button onClick={accept} disabled={!checked || busy || declined}>
            {busy ? "Saving…" : RISK_ACK_CTA}
          </Button>
          <Button onClick={() => setDeclined(true)} disabled={declined}>
            {RISK_ACK_DECLINE_LABEL}
          </Button>
          <button
            type="button"
            onClick={() => void logout()}
            className="ml-auto text-[12px] text-[var(--text-dim)] underline hover:text-[var(--text)]"
          >
            Sign out
          </button>
        </div>
      </div>
    </div>
  );
}
