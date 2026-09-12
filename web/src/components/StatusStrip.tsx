"use client";

// StatusStrip — one compact row of platform states, replacing a grid of
// large status cards (T-087).
//
// The audit found Overview leading with twelve status cards, then six
// session-counter cards, then more current-state and exchange-health
// cards, so the question "is the platform connected and is simulation
// running" cost a scroll and a scan. This renders the same states in one
// 2-line row and gives the space back to the working data.
//
// Rules it enforces, each for a reason:
//
//   * The **word** carries the state, never the dot. `DEGRADED`,
//     `CONNECTED`, `IDLE` are the backend's own vocabulary, rendered as
//     text, so the strip is readable in greyscale, by a colour-blind
//     reader, and by a screen reader.
//   * `unknown` uses a **hollow** dot, not just a grey one — a shape
//     difference, so "we don't know" cannot be mistaken for "fine" when
//     colour is unavailable.
//   * `loading` renders a literal `…` with `aria-busy`, never a
//     fabricated value and never a placeholder that looks like data.
//   * Segments keep the order the caller declared and never re-sort on a
//     poll, so a figure does not move under a pointer mid-click.
//   * It never scrolls sideways and never ellipsises a state word: it
//     reflows to fewer columns, then to a definition list.

import Link from "next/link";
import type { ReactNode } from "react";

export type StatusTone = "ok" | "warn" | "bad" | "unknown";

export interface StatusSegment {
  // label: short and stable — this is the row header, not a sentence.
  label: string;
  // word: the state itself, in the backend's own vocabulary where there
  // is one. null means genuinely unknown, which is rendered as such
  // rather than as a default or a zero.
  word: string | null;
  tone: StatusTone;
  // detail: a short qualifier shown under the word (an age, an offset, a
  // count). Dropped before the word if space runs out.
  detail?: string;
  // title: the full explanation, for the cases where the backend hands
  // back a long reason. Supplementary only.
  title?: string;
  href?: string;
  loading?: boolean;
}

function toneColor(tone: StatusTone): string {
  switch (tone) {
    case "ok":
      return "var(--ok)";
    case "warn":
      return "var(--warn)";
    case "bad":
      return "var(--critical)";
    case "unknown":
      return "var(--text-dim)";
  }
}

// Dot is decorative reinforcement of the word, never the state itself.
// The hollow variant is what distinguishes `unknown` without colour.
function Dot({ tone }: { tone: StatusTone }) {
  const color = toneColor(tone);
  return (
    <span
      aria-hidden
      className="inline-block h-2 w-2 shrink-0 rounded-full"
      style={
        tone === "unknown"
          ? { border: `1.5px solid ${color}`, background: "transparent" }
          : { background: color }
      }
    />
  );
}

function SegmentBody({ seg }: { seg: StatusSegment }) {
  const color = toneColor(seg.tone);
  return (
    <>
      <div className="text-[11px] uppercase leading-[14px] tracking-wider text-[var(--text-dim)]">
        {seg.label}
      </div>
      {seg.loading ? (
        <div
          aria-busy
          className="text-[13px] font-semibold leading-[18px] text-[var(--text-dim)]"
        >
          …
        </div>
      ) : (
        <div className="flex items-center gap-1.5">
          <Dot tone={seg.tone} />
          <span
            className="text-[13px] font-semibold leading-[18px]"
            style={{ color }}
          >
            {seg.word ?? "unknown"}
          </span>
        </div>
      )}
      {seg.detail && !seg.loading && (
        <div className="truncate text-[11px] leading-[14px] text-[var(--text-dim)]">
          {seg.detail}
        </div>
      )}
    </>
  );
}

// rollUp summarises the strip in one phrase, announced before the
// segments so assistive technology gets the answer first rather than
// after six readings.
function rollUp(segments: StatusSegment[]): { text: string; tone: StatusTone } {
  const settled = segments.filter((s) => !s.loading);
  if (settled.length === 0) return { text: "CHECKING", tone: "unknown" };
  const bad = settled.filter((s) => s.tone === "bad").length;
  const warn = settled.filter((s) => s.tone === "warn").length;
  const unknown = settled.filter((s) => s.tone === "unknown").length;
  if (bad > 0) return { text: `${bad} CRITICAL`, tone: "bad" };
  if (warn > 0) return { text: `${warn} DEGRADED`, tone: "warn" };
  if (unknown > 0) return { text: `${unknown} UNKNOWN`, tone: "unknown" };
  return { text: "ALL OK", tone: "ok" };
}

export function StatusStrip({
  segments,
  label,
}: {
  segments: StatusSegment[];
  // label: names the group, so the strip is not an unlabelled cluster of
  // figures in the accessibility tree.
  label: string;
}) {
  const summary = rollUp(segments);
  const spoken = segments
    .filter((s) => !s.loading)
    .map((s) => `${s.label}: ${s.word ?? "unknown"}${s.detail ? `, ${s.detail}` : ""}`)
    .join(". ");

  return (
    <div
      role="group"
      aria-label={label}
      className="mb-4 rounded border border-[var(--border)] bg-[var(--bg-panel)]"
    >
      {/* The summary sentence, announced before the individual segments. */}
      <p className="sr-only">
        {label}: {summary.text}. {spoken}
      </p>
      <div className="grid grid-cols-2 gap-x-4 gap-y-2 px-3 py-2 sm:grid-cols-3 lg:flex lg:items-start lg:gap-6">
        <div className="min-w-0">
          <div className="text-[11px] uppercase leading-[14px] tracking-wider text-[var(--text-dim)]">
            Platform
          </div>
          <div className="flex items-center gap-1.5">
            <Dot tone={summary.tone} />
            <span
              className="text-[13px] font-semibold leading-[18px]"
              style={{ color: toneColor(summary.tone) }}
            >
              {summary.text}
            </span>
          </div>
        </div>
        {segments.map((seg) => {
          const body = <SegmentBody seg={seg} />;
          return (
            <div key={seg.label} className="min-w-0" title={seg.title}>
              {seg.href && !seg.loading ? (
                <Link
                  href={seg.href}
                  className="block rounded hover:bg-[var(--bg-raised)]"
                >
                  {body}
                </Link>
              ) : (
                body
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ---- Attention ----------------------------------------------------------

export interface AttentionItem {
  // id: stable, so the list does not reorder identity under a poll.
  id: string;
  // text: what is wrong, in plain language. Where the backend supplies
  // the reason, it is rendered verbatim inside `detail` rather than
  // paraphrased here.
  text: string;
  detail?: string;
  tone: "warn" | "bad";
  // action: where to go to deal with it. Every item has one — an
  // attention list with no next step is just a worry list.
  action: { href: string; label: string };
}

// AttentionList answers "what needs my attention, and what can I safely
// do next". There is no backend field for this: the backend review
// confirmed it is composed from `/alerts?state=active`, `/risk` breakers
// and `/system/health`, so the composition happens here and each item
// names the source it came from.
//
// The empty state is a real answer, not a blank: it says nothing needs
// attention and still offers somewhere useful to go.
export function AttentionList({
  items,
  loading,
  emptyAction,
}: {
  items: AttentionItem[];
  loading?: boolean;
  emptyAction?: ReactNode;
}) {
  if (loading && items.length === 0) {
    return (
      <p aria-busy className="text-[13px] text-[var(--text-dim)]">
        Checking for anything that needs attention…
      </p>
    );
  }
  if (items.length === 0) {
    return (
      <div className="flex flex-wrap items-center gap-3 rounded border border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 text-[13px]">
        <span className="flex items-center gap-1.5">
          <Dot tone="ok" />
          <span className="text-[var(--text)]">Nothing needs attention.</span>
        </span>
        {emptyAction}
      </div>
    );
  }
  return (
    <ul className="space-y-1.5">
      {items.map((item) => (
        <li
          key={item.id}
          className="flex flex-wrap items-start gap-x-3 gap-y-1 rounded border-l-[3px] border border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 text-[13px]"
          style={{
            borderLeftColor:
              item.tone === "bad" ? "var(--critical)" : "var(--warn)",
          }}
        >
          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="text-[var(--text)]">{item.text}</span>
            {item.detail && (
              // Backend-supplied reasons render verbatim — never
              // paraphrased into something friendlier that means
              // something slightly different.
              <span className="break-words text-[12px] text-[var(--text-dim)]">
                {item.detail}
              </span>
            )}
          </span>
          <Link
            href={item.action.href}
            className="shrink-0 font-medium text-[var(--accent)] underline"
          >
            {item.action.label} →
          </Link>
        </li>
      ))}
    </ul>
  );
}
