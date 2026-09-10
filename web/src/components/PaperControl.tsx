"use client";

// PaperControl is the one pause/resume surface for the paper engine,
// shared between the app shell (reachable from every page, including
// mobile, without navigating to /paper — audit F2) and the /paper page's
// own Engine section. One implementation means one confirm flow, one
// error-reporting convention and one RBAC check, instead of the shell
// and the page drifting apart.
//
// Pausing gets a single confirmation step: the consequence (in-flight
// simulations keep settling to their own outcome; nothing already filled
// is cancelled; only new cycles stop starting) is not obvious from the
// button alone. Resuming is a single click — it only allows new cycles
// to start again, nothing is put at risk. Every result, success or
// failure, is reported through a toast that names the backend's own
// message (and HTTP status on failure) instead of a generic sentence.
// VIEWERs see the live state text with no button — the backend enforces
// paper:control server-side regardless; this only hides what a VIEWER
// cannot use.

import { useState } from "react";
import { api, ApiError, type ScannerStatus } from "@/lib/api/client";
import type { PollState } from "@/lib/usePoll";
import { can } from "@/lib/auth";
import { Button, ConfirmDialog } from "@/components/ui";
import { useToast } from "@/components/Toast";

export function PaperControl({
  status,
  role,
  compact,
  hideStateLabel,
}: {
  status: PollState<ScannerStatus>;
  role: string | undefined;
  // compact: the mobile top-bar row — smaller type, no wrapping label.
  compact?: boolean;
  // hideStateLabel: the /paper page's own Engine section already shows a
  // "State" stat cell right above this control — repeating "PAPER
  // RUNNING/PAUSED" there would be redundant, not just differently sized.
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
          ? "Paper trading resumed — new cycles will start again."
          : "Paper trading paused — in-flight simulations settle; no new cycles start.",
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
  const stateText = paper.running ? "PAPER RUNNING" : "PAPER PAUSED";

  return (
    <div
      className={
        compact
          ? "flex min-w-0 items-center gap-1.5"
          : "mb-3 flex flex-wrap items-center gap-2"
      }
    >
      {!hideStateLabel && (
        <span
          className={`shrink-0 font-semibold ${compact ? "text-[10px]" : "text-[11px]"}`}
          style={{ color: stateColor }}
        >
          {stateText}
        </span>
      )}
      {mayControl && (
        <Button
          onClick={() => (paper.running ? setConfirmOpen(true) : run("resume"))}
          disabled={busy}
          danger={paper.running}
        >
          {busy
            ? paper.running
              ? "Pausing…"
              : "Resuming…"
            : paper.running
              ? "Pause paper trading"
              : "Resume paper trading"}
        </Button>
      )}
      {confirmOpen && (
        <ConfirmDialog
          title="Pause paper trading?"
          danger
          confirmLabel={busy ? "Pausing…" : "Pause paper trading"}
          confirmDisabled={busy}
          onConfirm={() => run("pause")}
          onCancel={() => setConfirmOpen(false)}
          body={
            <p>
              In-flight simulations settle to their own outcome — nothing already
              filled is cancelled. No new cycle starts until you resume.
            </p>
          }
        />
      )}
    </div>
  );
}
