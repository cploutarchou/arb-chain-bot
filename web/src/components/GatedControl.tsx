"use client";

// GatedControl — the three "can't touch this" treatments (design-system.md
// §1.6, UX §2.4): a nav item, filter chip, or button can be unbuilt
// (no page yet), role-restricted (this operator's role can't reach it), or
// package-gated (a client-console entitlement check). All three stay
// visually and textually distinct — package is the one clickable state
// (it is a sales surface, not a dead end) and never renders in the
// operator console (UX §4.2: "the operator console never renders
// `package` since it has every package").
//
// Package-gating is display-only: the backend's own entitlement/limit
// check is the real gate regardless of what this component shows
// (design-system.md §2.4 / client-area.md RBAC principle).

import Link from "next/link";
import type { ReactNode } from "react";
import { Badge } from "@/components/ui";
import { LockIcon } from "@/components/icons";

export type GateState = "unbuilt" | "role" | "package";

export interface GatedControlProps {
  state: GateState;
  // Tooltip text for unbuilt/role ("Not implemented yet" / "Requires
  // OPERATOR+"); ignored for package (which uses packageName instead).
  reason: string;
  as: "nav" | "chip" | "button";
  children: ReactNode;
  // Leading icon (nav's NavIcon, or a chip's own glyph) — only nav dims it
  // to 0.6 opacity in the package state per §1.6.
  icon?: ReactNode;
  // Required for state="package": the upgrade/billing route the whole
  // control links to, and the package name shown in the chip/badge.
  upgradeHref?: string;
  packageName?: string;
}

export function GatedControl({
  state,
  reason,
  as,
  children,
  icon,
  upgradeHref,
  packageName,
}: GatedControlProps) {
  if (state === "package") {
    const pkg = packageName ?? "a higher package";
    const title = `Included in ${pkg} — Upgrade`;
    const lockLabel = `Included in ${pkg} — upgrade`;
    const href = upgradeHref ?? "#";
    if (as === "nav") {
      return (
        <Link
          href={href}
          title={title}
          className="flex items-center gap-2 rounded px-2 py-1 text-[var(--text-gated)] hover:bg-[var(--bg-raised)]"
        >
          {icon && <span className="opacity-60">{icon}</span>}
          <span className="flex-1">{children}</span>
          <LockIcon label={lockLabel} />
          <Badge tone="dim">{pkg}</Badge>
        </Link>
      );
    }
    if (as === "chip") {
      return (
        <Link
          href={href}
          title={title}
          className="inline-flex items-center gap-1 rounded border border-[var(--border-strong)] px-2 py-0.5 text-[12px] text-[var(--text-gated)] hover:bg-[var(--bg-raised)]"
        >
          <LockIcon label={lockLabel} />
          {children}
        </Link>
      );
    }
    // as === "button"
    return (
      <Link
        href={href}
        title={title}
        className="inline-flex items-center gap-1.5 rounded border border-[var(--border-strong)] px-2.5 py-1 text-[12px] font-medium text-[var(--text-gated)] hover:bg-[var(--bg-raised)]"
      >
        <LockIcon label={lockLabel} />
        {children}
      </Link>
    );
  }

  // unbuilt / role — identical visual treatment, different tooltip text
  // (the caller supplies the exact `reason`, e.g. "Requires OPERATOR+",
  // never a generic "restricted").
  if (as === "nav") {
    return (
      <span
        title={reason}
        aria-disabled="true"
        className="flex cursor-not-allowed items-center gap-2 rounded px-2 py-1 text-[var(--text-dim)] opacity-[var(--opacity-disabled)]"
      >
        {icon}
        {children}
      </span>
    );
  }
  if (as === "chip") {
    return (
      <span
        title={reason}
        aria-disabled="true"
        className="inline-flex cursor-not-allowed items-center gap-1 rounded border border-[var(--border)] px-2 py-0.5 text-[12px] text-[var(--text-dim)] opacity-[var(--opacity-disabled)]"
      >
        {children}
      </span>
    );
  }
  // as === "button" — same shape as ui.tsx's Button(disabled), title
  // carries the reason since Button itself takes no title prop.
  return (
    <span title={reason} className="inline-block">
      <button
        type="button"
        disabled
        aria-disabled="true"
        className="cursor-not-allowed rounded border border-[var(--border)] px-2.5 py-1 text-[12px] font-medium text-[var(--text-dim)] opacity-[var(--opacity-disabled)]"
      >
        {children}
      </button>
    </span>
  );
}
