"use client";

// Organisation (T-081/T-082, docs/design/billing.md §1.1/§1.4): name,
// package, members and risk-acknowledgement status. Membership mutations
// (invite/role/remove) need OWNER or ADMIN (internal/api/orgapi.go
// requireOrgManager — platform admins also pass); everyone else sees the
// roster read-only. There is no email-invite endpoint: POST
// /org/members takes an existing account's user_id
// (internal/api/orgapi.go handleOrgMemberAdd) because self-service
// e-mail invites (account creation from inside the console) land with
// T-085; /api/v1/users, which does create accounts by e-mail, is
// platform-admin only.

import { useState, type FormEvent } from "react";
import Link from "next/link";
import {
  api,
  ApiError,
  type OrgGetResponse,
  type OrgMember,
  type OrgRole,
} from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  Await,
  Badge,
  Button,
  ConfirmDialog,
  PageTitle,
  Section,
  Table,
  fmtTime,
} from "@/components/ui";

const ROLES: OrgRole[] = ["OWNER", "ADMIN", "MEMBER", "VIEWER"];

function riskAckBadge(org: OrgGetResponse["org"]) {
  if (!org.risk_ack_version) {
    return <Badge tone="warn">Not acknowledged</Badge>;
  }
  return (
    <Badge tone="ok">
      v{org.risk_ack_version}
      {org.risk_ack_at ? ` — ${fmtTime(org.risk_ack_at)}` : ""}
    </Badge>
  );
}

function OrgOverview({ data }: { data: OrgGetResponse }) {
  const ent = data.entitlements;
  return (
    <Section title="Organisation">
      <div className="grid max-w-2xl grid-cols-2 gap-3 text-[13px]">
        <div>
          <div className="text-[12px] text-[var(--text-dim)]">Name</div>
          <div className="text-[var(--text)]">{data.org.name}</div>
        </div>
        <div>
          <div className="text-[12px] text-[var(--text-dim)]">Package</div>
          <div className="text-[var(--text)]">
            {ent.package_code}
            {ent.status?.subscription && (
              <span className="ml-1 text-[12px] text-[var(--text-dim)]">
                ({ent.status.subscription}
                {ent.status.read_only ? ", read-only" : ""})
              </span>
            )}
          </div>
        </div>
        <div>
          <div className="text-[12px] text-[var(--text-dim)]">Your role</div>
          <div className="text-[var(--text)]">{data.org_role}</div>
        </div>
        <div>
          <div className="text-[12px] text-[var(--text-dim)]">
            Risk Disclosure
          </div>
          <div>{riskAckBadge(data.org)}</div>
        </div>
      </div>
      <p className="mt-3 text-[12px] text-[var(--text-dim)]">
        <Link href="/billing" className="text-[var(--accent)] underline">
          Manage package and billing →
        </Link>
      </p>
    </Section>
  );
}

function MembersManager({
  canManage,
  selfId,
}: {
  canManage: boolean;
  selfId: string | undefined;
}) {
  const [refresh, setRefresh] = useState(0);
  const members = usePoll(() => api.org.members(), 15000, [refresh]);
  const bump = () => setRefresh((n) => n + 1);

  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  // Invite form (by existing account's user_id — see file header).
  const [inviteUserId, setInviteUserId] = useState("");
  const [inviteRole, setInviteRole] = useState<OrgRole>("VIEWER");
  const [inviteErr, setInviteErr] = useState("");
  const [inviting, setInviting] = useState(false);

  const [roleDialog, setRoleDialog] = useState<{
    member: OrgMember;
    role: OrgRole;
  } | null>(null);
  const [removeDialog, setRemoveDialog] = useState<OrgMember | null>(null);
  const [actionErr, setActionErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submitInvite = async (e: FormEvent) => {
    e.preventDefault();
    setInviteErr("");
    if (!inviteUserId.trim()) {
      setInviteErr("User ID is required.");
      return;
    }
    setInviting(true);
    try {
      await api.org.addMember(inviteUserId.trim(), inviteRole);
      setMsg({
        ok: true,
        text: `${inviteUserId.trim()} added as ${inviteRole}.`,
      });
      setInviteUserId("");
      setInviteRole("VIEWER");
      bump();
    } catch (err: unknown) {
      setInviteErr(
        err instanceof ApiError ? err.message : "Add member failed.",
      );
    } finally {
      setInviting(false);
    }
  };

  const confirmRoleChange = async () => {
    if (!roleDialog) return;
    setActionErr("");
    setBusy(true);
    try {
      await api.org.setMemberRole(roleDialog.member.user_id, roleDialog.role);
      setMsg({
        ok: true,
        text: `${roleDialog.member.email ?? roleDialog.member.user_id} is now ${roleDialog.role}.`,
      });
      setRoleDialog(null);
      bump();
    } catch (err: unknown) {
      setActionErr(
        err instanceof ApiError ? err.message : "Role change failed.",
      );
      setRoleDialog(null);
    } finally {
      setBusy(false);
    }
  };

  const confirmRemove = async () => {
    if (!removeDialog) return;
    setActionErr("");
    setBusy(true);
    try {
      await api.org.removeMember(removeDialog.user_id);
      setMsg({
        ok: true,
        text: `${removeDialog.email ?? removeDialog.user_id} removed.`,
      });
      setRemoveDialog(null);
      bump();
    } catch (err: unknown) {
      setActionErr(err instanceof ApiError ? err.message : "Remove failed.");
      setRemoveDialog(null);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section title="Members">
      {msg && (
        <p
          className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
        >
          {msg.text}
        </p>
      )}
      <Await state={members} what="members">
        {(res) => (
          <>
            <p className="mb-2 text-[12px] text-[var(--text-dim)]">
              Seats: {res.members?.length ?? 0} /{" "}
              {res.seats_max === -1 ? "unlimited" : res.seats_max}
            </p>
            <Table
              head={["Email", "Role", "Status", "Joined", ""]}
              empty="members"
              rows={(res.members ?? []).map((m) => [
                m.email ?? m.user_id,
                canManage ? (
                  <select
                    key="role"
                    value={m.role}
                    disabled={m.user_id === selfId}
                    title={
                      m.user_id === selfId
                        ? "You cannot change your own role."
                        : undefined
                    }
                    onChange={(e) =>
                      setRoleDialog({
                        member: m,
                        role: e.target.value as OrgRole,
                      })
                    }
                    className="rounded border border-[var(--border)] bg-[var(--bg)] px-1.5 py-0.5 text-[12px] disabled:opacity-50"
                  >
                    {ROLES.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </select>
                ) : (
                  m.role
                ),
                <Badge key="s" tone={m.suspended ? "warn" : "ok"}>
                  {m.suspended ? "Suspended" : "Active"}
                </Badge>,
                fmtTime(m.created_at),
                canManage ? (
                  <Button
                    key="remove"
                    onClick={() => setRemoveDialog(m)}
                    disabled={m.user_id === selfId}
                    danger
                  >
                    Remove
                  </Button>
                ) : (
                  ""
                ),
              ])}
            />
          </>
        )}
      </Await>

      {canManage && (
        <div className="mt-4 max-w-sm rounded border border-[var(--border)] p-3">
          <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
            Add member
          </h3>
          <p className="mb-2 text-[12px] text-[var(--text-dim)]">
            Adds an existing account to this organisation by user ID. E-mail
            invitation for new accounts is not available yet.
          </p>
          <form onSubmit={submitInvite} className="space-y-2">
            <div>
              <label
                className="mb-1 block text-[12px] text-[var(--text-dim)]"
                htmlFor="invite-user-id"
              >
                User ID
              </label>
              <input
                id="invite-user-id"
                required
                value={inviteUserId}
                onChange={(e) => setInviteUserId(e.target.value)}
                className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
              />
            </div>
            <div>
              <label
                className="mb-1 block text-[12px] text-[var(--text-dim)]"
                htmlFor="invite-role"
              >
                Role
              </label>
              <select
                id="invite-role"
                value={inviteRole}
                onChange={(e) => setInviteRole(e.target.value as OrgRole)}
                className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none"
              >
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </div>
            {inviteErr && (
              <p className="text-[var(--critical)] text-[12px]">{inviteErr}</p>
            )}
            <Button type="submit" onClick={() => undefined} disabled={inviting}>
              {inviting ? "Adding…" : "Add member"}
            </Button>
          </form>
        </div>
      )}

      {roleDialog && (
        <ConfirmDialog
          title={`Change ${roleDialog.member.email ?? roleDialog.member.user_id} to ${roleDialog.role}?`}
          confirmLabel={busy ? "Saving…" : "Change role"}
          confirmDisabled={busy}
          onConfirm={confirmRoleChange}
          onCancel={() => setRoleDialog(null)}
          body={
            <div>
              {actionErr && (
                <p className="text-[var(--critical)]">{actionErr}</p>
              )}
            </div>
          }
        />
      )}
      {removeDialog && (
        <ConfirmDialog
          title={`Remove ${removeDialog.email ?? removeDialog.user_id}?`}
          danger
          confirmLabel={busy ? "Removing…" : "Remove"}
          confirmDisabled={busy}
          onConfirm={confirmRemove}
          onCancel={() => setRemoveDialog(null)}
          body={
            <div>
              {actionErr && (
                <p className="text-[var(--critical)]">{actionErr}</p>
              )}
            </div>
          }
        />
      )}
    </Section>
  );
}

export default function OrgPage() {
  const { state: auth } = useAuth();
  const [refresh] = useState(0);
  const org = usePoll(() => api.org.get(), 30000, [refresh]);

  const selfId = auth.kind === "authenticated" ? auth.me.user_id : undefined;
  const platformAdmin = auth.kind === "authenticated" && auth.me.platform_admin;

  return (
    <ConsoleShell active="Organisation">
      <PageTitle>Organisation</PageTitle>
      <Await state={org} what="organisation">
        {(data) => {
          const canManage =
            platformAdmin ||
            data.org_role === "OWNER" ||
            data.org_role === "ADMIN";
          return (
            <>
              <OrgOverview data={data} />
              <MembersManager canManage={canManage} selfId={selfId} />
            </>
          );
        }}
      </Await>
    </ConsoleShell>
  );
}
