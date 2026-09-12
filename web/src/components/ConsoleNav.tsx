"use client";

// Console navigation: the primary destination list, the contextual
// secondary list for whichever destination is open, and the breadcrumb
// trail (T-087).
//
// Kept out of ConsoleShell.tsx deliberately. The shell was 872 lines
// holding the navigation data, the mode banner, the restart dialog and
// the layout all at once; the audit's navigation findings are addressed
// here, and the navigation data itself lives in lib/nav.ts.
//
// Two rules this file exists to enforce:
//
//   * Selection comes from the URL, never from display text. Every entry
//     has a stable id and `resolveNav(pathname)` decides what is current,
//     so a copy change cannot break the highlight, the icon or a test.
//     Three pages highlighted nothing under the old label-matching shell.
//   * Privileged navigation never flashes. While the session is still
//     loading, nothing role- or entitlement-gated renders at all — not
//     even as a gated placeholder. An operator-administration entry that
//     appears for a moment and then vanishes tells an ordinary user
//     something untrue about their own account.
//
// Visibility here is presentation. The backend remains the only real
// gate (AGENTS.md: "RBAC is enforced in the backend, never by hiding
// buttons"), and every endpoint behind these entries re-checks.

import Link from "next/link";
import { usePathname } from "next/navigation";
import { NAV, resolveNav, type NavDestination, type NavLeaf } from "@/lib/nav";
import { can, useAuth, useEntitlement } from "@/lib/auth";
import { NavIcon } from "@/components/icons";
import { GatedControl } from "@/components/GatedControl";

// ---- Visibility ----------------------------------------------------------

type LeafState =
  | { kind: "link" }
  | { kind: "hidden" }
  | {
      kind: "gated";
      state: "role" | "package";
      reason: string;
      packageName?: string;
    };

function useNavVisibility() {
  const { state } = useAuth();
  const authenticated = state.kind === "authenticated";
  const role = authenticated ? state.me.role : undefined;
  // platform_admin is the only thing that opens platform configuration.
  // A tenant's ADMIN display role administers their own organisation and
  // never reaches platform settings on the strength of that label.
  const platformAdmin = authenticated && state.me.platform_admin === true;

  // undefined while the session loads — never gate or ungate optimistically.
  const autoPaperStrategies = useEntitlement("auto_paper.strategies");

  const leafState = (leaf: NavLeaf): LeafState => {
    if (leaf.contextual) return { kind: "hidden" };
    const access = leaf.access;
    if (!access) return { kind: "link" };
    switch (access.kind) {
      case "member":
        return { kind: "link" };
      case "platform":
        // Hidden rather than gated: a tenant has no upgrade path to
        // platform staff, so an "upgrade" or "requires" annotation here
        // would be noise about something that is not theirs to reach.
        return platformAdmin ? { kind: "link" } : { kind: "hidden" };
      case "perm":
        // Still loading: render nothing rather than a wrong answer.
        if (!authenticated) return { kind: "hidden" };
        return can(role, access.perm)
          ? { kind: "link" }
          : { kind: "gated", state: "role", reason: access.minRoleLabel };
      case "entitlement": {
        if (autoPaperStrategies === undefined) return { kind: "hidden" };
        return autoPaperStrategies.length === 0
          ? {
              kind: "gated",
              state: "package",
              reason: "",
              packageName: access.packageName,
            }
          : { kind: "link" };
      }
    }
  };

  // A destination appears when at least one of its entries does, so an
  // area never renders as an empty shell.
  const destinations = NAV.filter((d) => {
    if (d.access?.kind === "platform" && !platformAdmin) return false;
    return d.groups.some((g) =>
      g.items.some((leaf) => leafState(leaf).kind !== "hidden"),
    );
  });

  return { destinations, leafState };
}

// useCurrentNav resolves the current route for the shell.
export function useCurrentNav() {
  const pathname = usePathname() ?? "/";
  return resolveNav(pathname);
}

// ---- Primary navigation --------------------------------------------------

// PrimaryNav renders the primary destinations. It never scrolls: the
// primary choices must all be visible without scrolling at 1440×900, so
// the shell gives this block a fixed place and lets only the secondary
// list below it scroll.
export function PrimaryNav({ onNavigate }: { onNavigate?: () => void }) {
  const { destinations } = useNavVisibility();
  const current = useCurrentNav();
  const product = destinations.filter((d) => d.id !== "operator");
  const operator = destinations.filter((d) => d.id === "operator");

  const item = (d: NavDestination) => {
    const active = current?.destination.id === d.id;
    // A destination that *contains* the current page is "location"; the
    // page itself is "page". That is the correct ARIA distinction, and
    // it is why the state is not carried by colour alone.
    const isLanding = active && current?.leaf.href === d.href;
    return (
      <li key={d.id}>
        <Link
          href={d.href}
          onClick={onNavigate}
          aria-current={active ? (isLanding ? "page" : "location") : undefined}
          data-nav-id={d.id}
          title={d.description}
          className={`flex items-center gap-2.5 rounded px-2 py-1.5 text-[13px] ${
            active
              ? "bg-[var(--bg-raised)] font-medium text-[var(--text)] shadow-[inset_2px_0_0_0_var(--accent)]"
              : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
          }`}
        >
          <NavIcon label={d.icon} />
          <span className="min-w-0 truncate">{d.label}</span>
        </Link>
      </li>
    );
  };

  return (
    <nav aria-label="Primary" className="shrink-0">
      <ul className="space-y-0.5">{product.map(item)}</ul>
      {operator.length > 0 && (
        // Explicitly labelled and visually separated, so nobody has to
        // work out that these surfaces concern the platform rather than
        // their own organisation.
        <div className="mt-3 border-t border-[var(--border)] pt-2">
          <h2 className="mb-1 px-2 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Operator
          </h2>
          <ul className="space-y-0.5">{operator.map(item)}</ul>
        </div>
      )}
    </nav>
  );
}

// ---- Secondary (contextual) navigation -----------------------------------

// SecondaryNav lists the surfaces inside the destination that is open.
// This is the half that replaces the previous wall: instead of 29 links
// at once, a person sees the primary choices plus the handful belonging
// to wherever they currently are.
export function SecondaryNav({ onNavigate }: { onNavigate?: () => void }) {
  const { leafState } = useNavVisibility();
  const current = useCurrentNav();
  if (!current) return null;
  const destination = current.destination;

  const rendered = destination.groups
    .map((group) => ({
      group,
      items: group.items
        .map((leaf) => ({ leaf, state: leafState(leaf) }))
        .filter((x) => x.state.kind !== "hidden"),
    }))
    .filter((g) => g.items.length > 0);
  // A destination with a single entry that is the destination itself
  // needs no secondary list; repeating it would be noise.
  if (rendered.length === 0) return null;
  const only = rendered.length === 1 && rendered[0]?.items.length === 1;
  if (only && rendered[0]?.items[0]?.leaf.href === destination.href) return null;

  return (
    <nav
      aria-label={`${destination.label} sections`}
      // The only scrolling region in the sidebar. The primary list above
      // keeps its place; this list yields when a destination has many
      // sections (Operations has the most).
      className="mt-3 min-h-0 flex-1 overflow-y-auto border-t border-[var(--border)] pt-2"
    >
      <h2 className="mb-1 px-2 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
        In {destination.label}
      </h2>
      <div className="space-y-2">
        {rendered.map(({ group, items }, gi) => (
          <div key={group.title ?? `group-${gi}`}>
            {group.title && (
              <h3 className="mb-0.5 px-2 text-[10px] uppercase tracking-wide text-[var(--text-dim)] opacity-80">
                {group.title}
              </h3>
            )}
            <ul className="space-y-0.5">
              {items.map(({ leaf, state }) => {
                if (state.kind === "gated") {
                  return (
                    <li key={leaf.id}>
                      <GatedControl
                        as="nav"
                        state={state.state}
                        reason={state.reason}
                        upgradeHref={
                          state.state === "package" ? "/billing" : undefined
                        }
                        packageName={state.packageName}
                      >
                        {leaf.label}
                      </GatedControl>
                    </li>
                  );
                }
                const active = current.leaf.id === leaf.id;
                return (
                  <li key={leaf.id}>
                    <Link
                      href={leaf.href}
                      onClick={onNavigate}
                      aria-current={active ? "page" : undefined}
                      data-nav-id={leaf.id}
                      title={leaf.description}
                      className={`block rounded px-2 py-1 pl-3 text-[12.5px] leading-snug ${
                        active
                          ? "bg-[var(--bg-raised)] font-medium text-[var(--text)]"
                          : "text-[var(--text-dim)] hover:bg-[var(--bg-raised)] hover:text-[var(--text)]"
                      }`}
                    >
                      <span className="block min-w-0 truncate">
                        {leaf.label}
                      </span>
                    </Link>
                  </li>
                );
              })}
            </ul>
          </div>
        ))}
      </div>
    </nav>
  );
}

// ---- Breadcrumbs ---------------------------------------------------------

// Breadcrumbs name where a page sits, now that the sidebar no longer
// shows every link at once. They are also what makes a deep link or a
// bookmark self-explanatory: arriving cold at /cycles/<id>, the trail
// reads Paper Trading / Cycle detail.
export function Breadcrumbs({ fallbackLabel }: { fallbackLabel?: string }) {
  const current = useCurrentNav();
  if (!current) {
    // An unrecognised route says where it is not, rather than inventing
    // a position in the hierarchy.
    return fallbackLabel ? (
      <p className="mb-3 text-[11px] text-[var(--text-dim)]">{fallbackLabel}</p>
    ) : null;
  }
  const { destination, leaf } = current;
  const sameAsDestination = leaf.href === destination.href;
  return (
    <nav aria-label="Breadcrumb" className="mb-3">
      <ol className="flex flex-wrap items-center gap-1.5 text-[11px] text-[var(--text-dim)]">
        <li>
          <Link
            href={destination.href}
            className="rounded hover:text-[var(--text)] hover:underline"
          >
            {destination.label}
          </Link>
        </li>
        {!sameAsDestination && (
          <>
            <li aria-hidden className="opacity-60">
              /
            </li>
            <li className="text-[var(--text)]">{leaf.label}</li>
          </>
        )}
      </ol>
      {leaf.description && (
        <p className="mt-1 max-w-3xl text-[12px] text-[var(--text-dim)]">
          {leaf.description}
        </p>
      )}
    </nav>
  );
}
