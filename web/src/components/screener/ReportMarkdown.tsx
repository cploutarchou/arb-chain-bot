"use client";

// Minimal, dependency-free markdown-to-elements renderer for the stored
// screener-report markdown (report.Report.md — the same text filed under
// <recordings dir>/screener-reports/<date>/*.md). No markdown library, no
// dangerouslySetInnerHTML, no raw HTML passthrough: every line is parsed
// into a fixed, safe set of React elements (heading/paragraph/list/rule),
// and the one shape that isn't worth hand-parsing exactly — the report's
// pipe tables — renders as a monospace <pre> block, which preserves their
// column alignment without a table parser.

import type { ReactNode } from "react";

function renderBlocks(markdown: string): ReactNode[] {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n");
  const blocks: ReactNode[] = [];
  let tableBuf: string[] = [];
  let listBuf: string[] = [];
  let fenceBuf: string[] = [];
  let inFence = false;
  let key = 0;

  const preBlock = (id: string, lineBuf: string[]) => (
    <pre
      key={id}
      className="overflow-x-auto rounded border border-[var(--border)] bg-[var(--bg)] p-2 text-[12px] leading-relaxed"
    >
      {lineBuf.join("\n")}
    </pre>
  );
  const flushTable = () => {
    if (tableBuf.length === 0) return;
    blocks.push(preBlock(`table-${key++}`, tableBuf));
    tableBuf = [];
  };
  const flushFence = () => {
    if (fenceBuf.length === 0) return;
    blocks.push(preBlock(`fence-${key++}`, fenceBuf));
    fenceBuf = [];
  };
  const flushList = () => {
    if (listBuf.length === 0) return;
    blocks.push(
      <ul
        key={`list-${key++}`}
        className="list-disc space-y-0.5 pl-5 text-[13px]"
      >
        {listBuf.map((item, i) => (
          <li key={i}>{item}</li>
        ))}
      </ul>,
    );
    listBuf = [];
  };

  for (const raw of lines) {
    const trimmed = raw.trim();

    // Fenced code blocks render verbatim, preformatted — nothing inside
    // one is parsed as a heading/bullet/table, matching how any other
    // markdown renderer treats a fence.
    if (inFence) {
      if (trimmed.startsWith("```")) {
        inFence = false;
        flushFence();
      } else {
        fenceBuf.push(raw);
      }
      continue;
    }
    if (trimmed.startsWith("```")) {
      flushList();
      flushTable();
      inFence = true;
      continue;
    }

    if (trimmed.startsWith("|")) {
      flushList();
      tableBuf.push(raw);
      continue;
    }
    flushTable();

    if (trimmed === "") {
      flushList();
      continue;
    }

    const heading = /^(#{1,6})\s+(.*)$/.exec(trimmed);
    if (heading) {
      flushList();
      const level = heading[1]!.length;
      const text = heading[2]!;
      blocks.push(
        <p
          key={`h-${key++}`}
          className={
            level <= 2
              ? "mt-4 mb-1 text-sm font-semibold text-[var(--text)]"
              : "mt-3 mb-1 text-[13px] font-semibold text-[var(--text-dim)]"
          }
        >
          {text}
        </p>,
      );
      continue;
    }

    if (trimmed === "---" || trimmed === "***") {
      flushList();
      blocks.push(
        <hr key={`hr-${key++}`} className="my-3 border-[var(--border)]" />,
      );
      continue;
    }

    const bullet = /^[-*]\s+(.*)$/.exec(trimmed);
    if (bullet) {
      listBuf.push(bullet[1]!);
      continue;
    }

    blocks.push(
      <p key={`p-${key++}`} className="text-[13px] leading-relaxed">
        {trimmed}
      </p>,
    );
  }
  flushList();
  flushTable();
  // An unterminated fence (malformed/truncated markdown) still renders
  // what was captured rather than silently dropping it.
  flushFence();
  return blocks;
}

export function ReportMarkdown({ markdown }: { markdown: string }) {
  if (!markdown.trim()) {
    return (
      <p className="text-[13px] text-[var(--text-dim)]">
        No markdown stored for this report.
      </p>
    );
  }
  return <div className="space-y-1">{renderBlocks(markdown)}</div>;
}
