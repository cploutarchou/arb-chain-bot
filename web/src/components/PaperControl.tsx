"use client";

// PaperControl is the one pause/resume surface for the triangular paper
// engine, shared between the app shell (reachable from every page,
// including mobile, without navigating to /paper) and the /paper page's
// own section. One implementation means one confirm flow, one
// error-reporting convention and one RBAC check, instead of the shell
// and the page drifting apart.
//
// ---- Scope, verified against the backend (T-087) -------------------------
//
// This control was previously labelled "Pause paper trading", which
// overstates what it does. `POST /api/v1/paper/pause` reaches
// `PaperController` → `paperProxy` → `e.Paper()` → `*paper.Engine`
// (internal/paper/engine.go:275-276), and that is the **triangular**
// paper engine alone. Three consequences, all of them stated in the UI
// rather than left for the user to discover:
//
//   1. Rule-based automatic paper execution is NOT affected. The Scanner
//      Suite automation loop and `paperexec.Executor` have no dependency
//      on `internal/paper` and no pause route exists for them at all
//      (internal/api/screenerapi.go:61-101). Implying otherwise would
//      tell someone their simulated trading had stopped when it had not.
//   2. It is process-wide, not per-organisation — `paperGate` never
//      reads the principal's OrgID and one shared engine is wired in
//      (internal/app/components.go:587). So it is never "my simulations".
//   3. Pausing stops NEW cycles. A cycle already in flight can still
//      settle and credit the ledger (internal/paper/engine.go:1-32), so
//      it is not an instant freeze.
//
// A pause spanning both engines would need a backend change; it has not
// been made, and this control does not pretend to be one.
//
// Pausing gets a single confirmation step because none of the above is
// obvious from a button. Resuming is a single click — it only allows new
// cycles to start again, nothing is put at risk. Every result, success or
// failure, is reported through a toast carrying the backend's own message
// (and HTTP status on failure) rather than a generic sentence. VIEWERs see
// the live state text with no button; the backend enforces paper:control
// server-side regardless, so this only hides what a VIEWER cannot use.

import { useState } from "react";
import { api, ApiError, type ScannerStatus } from "@/lib/api/client";
import type { PollState } from "@/lib/usePoll";
import { can } from "@/lib/auth";
import { Button, ConfirmDialog } from "@/components/ui";
import { useToast } from "@/components/Toast";

// SCOPE_NOTE is the one-line scope statement, used as the control's
// tooltip and repeated in the confirmation dialog. Kept in one constant
// so the two can never drift into saying different things.
const SCOPE_NOTE =
  "Covers the triangular paper engine only. Rule-based automatic paper execution keeps running — it has no pause control.";

export function PaperControl({
  status,
  role,
  compact,
  hideStateLabel,
}: {
  status: PollState<ScannerStatus>;
  role: string | undefined;
  // compact: the mobile top-bar row — smaller type, no scope line.
  compact?: boolean;
  // hideStateLabel: the /paper page's own section already shows a
  // "State" cell right above this control — repeating the state there
  // would be redundant, not just differently sized.
  hideStateLabel?: boolean;
}) {
  const toast = useToast();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [busy, setBusy] = useState(false);

  // Chrome-level: degrade quietly like ModeBanner/RestartBanner — a
  // loading flicker or a status-poll failure never blocks the rest of
  // the shell, and a profile with no paper engine (mode isn't PAPER)
  // shows nothing here rather than a permanent "not available" notice on
  // every page. The /paper page itself still renders the real absence
  // copy from its own Await/Section.
  if (status.kind !== "ready" || !status.data.paper) return null;

  const paper = status.data.paper;
  const mayControl = can(role, "paper:control");

  const run = async (action: "pause" | "resume") => {
    setBusy(true);
    try {
      const res =
        action === "pause" ? await api.paper.pause() : await api.paper.resume();
      toast.push({
        tone: "ok",
        text: res.running
          ? "Triangular simulations resumed — new cycles will start again."
          : "Triangular simulations paused — cycles already in flight settle; no new cycle starts. Rule simulations are unaffected.",
      });
    } catch (err: unknown) {
      const label = action === "pause" ? "Pause" : "Resume";
      toast.push({
        tone: "bad",
        text:
          err instanceof ApiError
            ? `${label} failed (HTTP ${err.status}): ${err.message}`
            : `${label} failed: backend unreachable.`,
      });
    } finally {
      setBusy(false);
      setConfirmOpen(false);
    }
  };

  const stateColor = paper.running ? "var(--ok)" : "var(--warn)";
  // The word carries the state, never the colour alone. "TRIANGULAR"
  // qualifies which engine this is, so the figure cannot be read as
  // covering every simulation on the platform.
  const stateText = paper.running
    ? "TRIANGULAR PAPER RUNNING"
    : "TRIANGULAR PAPER PAUSED";

  return (
    <div className={compact ? "flex min-w-0 items-center gap-1.5" : "mb-3"}>
      <div
        className={
          compact
            ? "flex min-w-0 items-center gap-1.5"
            : "flex flex-wrap items-center gap-2"
        }
      >
        {!hideStateLabel && (
          <span
            className={`font-semibold leading-snug ${compact ? "shrink-0 text-[10px]" : "text-[11px]"}`}
            style={{ color: stateColor }}
            title={SCOPE_NOTE}
          >
            {stateText}
          </span>
        )}
        {mayControl && (
          <Button
            onClick={() =>
              paper.running ? setConfirmOpen(true) : run("resume")
            }
            disabled={busy}
            danger={paper.running}
            title={SCOPE_NOTE}
          >
            {busy
              ? paper.running
                ? "Pausing…"
                : "Resuming…"
              : paper.running
                ? "Pause triangular simulations"
                : "Resume triangular simulations"}
          </Button>
        )}
      </div>
      {/* The scope is stated in text, not only in a tooltip — a tooltip
          is unreachable by touch and easy to miss with a pointer, and
          this is the sentence that stops someone believing all simulated
          trading has stopped. The compact mobile row has no space, so it
          keeps the tooltip and the dialog says it in full. */}
      {!compact && (
        <p className="mt-1 text-[10px] leading-snug text-[var(--text-dim)]">
          {SCOPE_NOTE}
        </p>
      )}
      {confirmOpen && (
        <ConfirmDialog
          title="Pause triangular simulations?"
          danger
          confirmLabel={busy ? "Pausing…" : "Pause triangular simulations"}
          confirmDisabled={busy}
          onConfirm={() => run("pause")}
          onCancel={() => setConfirmOpen(false)}
          body={
            <div className="space-y-2">
              <p>
                In-flight simulations settle to their own outcome — nothing
                already filled is cancelled. No new cycle starts until you
                resume.
              </p>
              <p>
                This covers the <strong>triangular</strong> paper engine only.
                Rule-based automatic paper execution is not affected and has no
                pause control of its own.
              </p>
              <p className="text-[var(--text-dim)]">
                The engine is shared across the whole deployment, so this
                affects every organisation using it, not just yours.
              </p>
            </div>
          }
        />
      )}
    </div>
  );
}
