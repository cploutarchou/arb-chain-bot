"use client";

import { useState, type FormEvent } from "react";
import Link from "next/link";
import {
  api,
  ApiError,
  isStaleVersion,
  staleVersion,
  type StrategyParams,
  type UserRow,
} from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { cloneParams, setPath, validateCooldown } from "@/lib/strategyFields";
import { diffParams, effectFor, type DiffRow } from "@/lib/diff";
import { ConsoleShell } from "@/components/ConsoleShell";
import { NotificationsFields } from "@/components/NotificationsFields";
import {
  AIAdvisorSection,
  LoggingAccessSection,
  MarketsSection,
  OperatingModeSection,
  VenuesSection,
  PlatformVersionHistorySection,
  TelegramAllowlistSection,
} from "@/components/PlatformSections";
import { SecretsSection } from "@/components/SecretsSection";
import { ScreenerSettingsSection } from "@/components/ScreenerSettingsSection";
import {
  Await,
  Badge,
  Button,
  ConfirmDialog,
  DiffTable,
  PageTitle,
  Section,
  StaleVersionNotice,
  Stat,
  Table,
  fmtTime,
} from "@/components/ui";

// ---- Session (top, always present) ----------------------------------------

function SessionSection() {
  const { state: auth, logout } = useAuth();
  const [showPwForm, setShowPwForm] = useState(false);
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setMsg(null);
    if (next.length < 12) {
      setMsg({
        ok: false,
        text: "New password must be at least 12 characters.",
      });
      return;
    }
    if (next !== confirm) {
      setMsg({
        ok: false,
        text: "New password and confirmation do not match.",
      });
      return;
    }
    setBusy(true);
    try {
      await api.auth.changePassword(current, next);
      setMsg({
        ok: true,
        text: "Password changed. You'll need it next time you sign in.",
      });
      setCurrent("");
      setNext("");
      setConfirm("");
      setShowPwForm(false);
    } catch (err: unknown) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Password change failed.",
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section title="Session">
      {auth.kind === "authenticated" ? (
        <div className="max-w-xl">
          <div className="flex items-center gap-3">
            <Stat label="User" value={auth.me.user_id} />
            <Stat
              label="Role"
              value={<Badge tone="ok">{auth.me.role}</Badge>}
            />
            <Button onClick={() => void logout()} danger>
              Sign out
            </Button>
            {!showPwForm && (
              <Button onClick={() => setShowPwForm(true)}>
                Change my password
              </Button>
            )}
          </div>
          {showPwForm && (
            <form
              onSubmit={submit}
              className="mt-3 max-w-sm space-y-2 rounded border border-[var(--border)] p-3"
            >
              <div>
                <label
                  className="mb-1 block text-[12px] text-[var(--text-dim)]"
                  htmlFor="pw-current"
                >
                  Current password
                </label>
                <input
                  id="pw-current"
                  type="password"
                  value={current}
                  onChange={(e) => setCurrent(e.target.value)}
                  className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
                />
              </div>
              <div>
                <label
                  className="mb-1 block text-[12px] text-[var(--text-dim)]"
                  htmlFor="pw-new"
                >
                  New password
                </label>
                <input
                  id="pw-new"
                  type="password"
                  value={next}
                  onChange={(e) => setNext(e.target.value)}
                  className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
                />
                <p className="mt-1 text-[11px] text-[var(--text-dim)]">
                  At least 12 characters.
                </p>
              </div>
              <div>
                <label
                  className="mb-1 block text-[12px] text-[var(--text-dim)]"
                  htmlFor="pw-confirm"
                >
                  Confirm new password
                </label>
                <input
                  id="pw-confirm"
                  type="password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
                />
              </div>
              <div className="flex gap-2">
                {/* Native <button> default type is "submit"; clicking inside
                    this <form> fires onSubmit above without a manual handler. */}
                <Button type="submit" onClick={() => undefined} disabled={busy}>
                  {busy ? "Changing…" : "Change password"}
                </Button>
                <Button onClick={() => setShowPwForm(false)} danger>
                  Cancel
                </Button>
              </div>
            </form>
          )}
          {msg && (
            <p
              className={`mt-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
            >
              {msg.text}
            </p>
          )}
        </div>
      ) : (
        <p className="text-sm text-[var(--text-dim)]">Not signed in.</p>
      )}
    </Section>
  );
}

// ---- Users & roles (BL-11) --------------------------------------------------

const ROLES: UserRow["role"][] = ["ADMIN", "OPERATOR", "VIEWER"];

function UsersSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const selfId = auth.kind === "authenticated" ? auth.me.user_id : undefined;
  const mayManage = can(role, "users:manage");
  // Users & Security manages the platform operator's own console
  // accounts (internal/api/usersapi.go: every route needs
  // users.platform_admin since T-081, not just the console ADMIN role —
  // a tenant OWNER/ADMIN's console role can equal ADMIN too, but that
  // never grants operator-staff account management).
  const platformAdmin = auth.kind === "authenticated" && auth.me.platform_admin;

  if (!mayManage || !platformAdmin) {
    return (
      <Section title="Users & roles">
        <p className="text-[13px] text-[var(--text-dim)]">
          Requires the platform operator (platform_admin).
        </p>
      </Section>
    );
  }
  return (
    <Section title="Users & roles">
      <UsersManager selfId={selfId} />
    </Section>
  );
}

function UsersManager({ selfId }: { selfId: string | undefined }) {
  const [refresh, setRefresh] = useState(0);
  const users = usePoll(() => api.users.list(), 15000, [refresh]);
  const [listMsg, setListMsg] = useState<{ ok: boolean; text: string } | null>(
    null,
  );

  // Create form
  const [email, setEmail] = useState("");
  const [newRole, setNewRole] = useState<UserRow["role"]>("VIEWER");
  const [password, setPassword] = useState("");
  const [confirmPw, setConfirmPw] = useState("");
  const [createErr, setCreateErr] = useState("");
  const [creating, setCreating] = useState(false);

  // Row-action dialogs
  const [roleDialog, setRoleDialog] = useState<{
    user: UserRow;
    role: UserRow["role"];
  } | null>(null);
  const [disableDialog, setDisableDialog] = useState<UserRow | null>(null);
  const [enableDialog, setEnableDialog] = useState<UserRow | null>(null);
  const [pwDialog, setPwDialog] = useState<UserRow | null>(null);
  const [pwValue, setPwValue] = useState("");
  const [pwConfirmValue, setPwConfirmValue] = useState("");
  const [actionErr, setActionErr] = useState("");
  const [busy, setBusy] = useState(false);

  const bump = () => setRefresh((n) => n + 1);

  const submitCreate = async (e: FormEvent) => {
    e.preventDefault();
    setCreateErr("");
    if (password.length < 12) {
      setCreateErr("Password must be at least 12 characters.");
      return;
    }
    if (password !== confirmPw) {
      setCreateErr("Passwords do not match.");
      return;
    }
    setCreating(true);
    try {
      const u = await api.users.create(email, newRole, password);
      setListMsg({ ok: true, text: `User ${u.email} created.` });
      setEmail("");
      setPassword("");
      setConfirmPw("");
      setNewRole("VIEWER");
      bump();
    } catch (err: unknown) {
      setCreateErr(
        err instanceof ApiError ? err.message : "Create user failed.",
      );
    } finally {
      setCreating(false);
    }
  };

  const confirmRoleChange = async () => {
    if (!roleDialog) return;
    setActionErr("");
    setBusy(true);
    try {
      await api.users.setRole(roleDialog.user.id, roleDialog.role);
      setListMsg({
        ok: true,
        text: `${roleDialog.user.email} is now ${roleDialog.role}.`,
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

  const confirmDisable = async () => {
    if (!disableDialog) return;
    setActionErr("");
    setBusy(true);
    try {
      await api.users.disable(disableDialog.id);
      setListMsg({ ok: true, text: `${disableDialog.email} disabled.` });
      setDisableDialog(null);
      bump();
    } catch (err: unknown) {
      setActionErr(err instanceof ApiError ? err.message : "Disable failed.");
      setDisableDialog(null);
    } finally {
      setBusy(false);
    }
  };

  const confirmEnable = async () => {
    if (!enableDialog) return;
    setActionErr("");
    setBusy(true);
    try {
      await api.users.enable(enableDialog.id);
      setListMsg({ ok: true, text: `${enableDialog.email} enabled.` });
      setEnableDialog(null);
      bump();
    } catch (err: unknown) {
      setActionErr(err instanceof ApiError ? err.message : "Enable failed.");
      setEnableDialog(null);
    } finally {
      setBusy(false);
    }
  };

  const openPwDialog = (u: UserRow) => {
    setPwValue("");
    setPwConfirmValue("");
    setActionErr("");
    setPwDialog(u);
  };

  const confirmPwReset = async () => {
    if (!pwDialog) return;
    if (pwValue.length < 12) {
      setActionErr("Password must be at least 12 characters.");
      return;
    }
    if (pwValue !== pwConfirmValue) {
      setActionErr("Passwords do not match.");
      return;
    }
    setBusy(true);
    try {
      await api.users.setPassword(pwDialog.id, pwValue);
      setListMsg({ ok: true, text: `Password reset for ${pwDialog.email}.` });
      setPwDialog(null);
      bump();
    } catch (err: unknown) {
      setActionErr(
        err instanceof ApiError ? err.message : "Password reset failed.",
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="max-w-4xl">
      {listMsg && (
        <p
          className={`mb-2 text-[13px] ${listMsg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
        >
          {listMsg.text}
        </p>
      )}
      <Await state={users} what="users">
        {(list) => (
          <Table
            head={["Email", "Role", "Status", "Created", ""]}
            empty="users"
            rows={list.map((u) => [
              u.email,
              <select
                key="role"
                value={u.role}
                disabled={u.id === selfId}
                title={
                  u.id === selfId
                    ? "You cannot change your own role — ask another ADMIN."
                    : undefined
                }
                onChange={(e) =>
                  setRoleDialog({
                    user: u,
                    role: e.target.value as UserRow["role"],
                  })
                }
                className="rounded border border-[var(--border)] bg-[var(--bg)] px-1.5 py-0.5 text-[12px] disabled:opacity-50"
              >
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>,
              <Badge key="s" tone={u.disabled ? "warn" : "ok"}>
                {u.disabled ? "Disabled" : "Active"}
              </Badge>,
              fmtTime(u.created_at),
              <div key="actions" className="flex gap-1.5">
                {u.disabled ? (
                  <Button onClick={() => setEnableDialog(u)}>Enable</Button>
                ) : (
                  <Button
                    onClick={() => setDisableDialog(u)}
                    disabled={u.id === selfId}
                    danger
                  >
                    Disable
                  </Button>
                )}
                <Button onClick={() => openPwDialog(u)}>Reset password</Button>
              </div>,
            ])}
          />
        )}
      </Await>

      <div className="mt-4 max-w-sm rounded border border-[var(--border)] p-3">
        <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
          Create user
        </h3>
        <form onSubmit={submitCreate} className="space-y-2">
          <div>
            <label
              className="mb-1 block text-[12px] text-[var(--text-dim)]"
              htmlFor="new-email"
            >
              Email
            </label>
            <input
              id="new-email"
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
            />
          </div>
          <div>
            <label
              className="mb-1 block text-[12px] text-[var(--text-dim)]"
              htmlFor="new-role"
            >
              Role
            </label>
            <select
              id="new-role"
              value={newRole}
              onChange={(e) => setNewRole(e.target.value as UserRow["role"])}
              className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none"
            >
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label
              className="mb-1 block text-[12px] text-[var(--text-dim)]"
              htmlFor="new-password"
            >
              Password
            </label>
            <input
              id="new-password"
              type="password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
            />
            <p className="mt-1 text-[11px] text-[var(--text-dim)]">
              At least 12 characters.
            </p>
          </div>
          <div>
            <label
              className="mb-1 block text-[12px] text-[var(--text-dim)]"
              htmlFor="new-password-confirm"
            >
              Confirm password
            </label>
            <input
              id="new-password-confirm"
              type="password"
              required
              value={confirmPw}
              onChange={(e) => setConfirmPw(e.target.value)}
              className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
            />
          </div>
          {createErr && (
            <p className="text-[12px] text-[var(--critical)]">{createErr}</p>
          )}
          {/* Native <button> default type is "submit"; clicking inside this
              <form> fires onSubmit above without a manual handler. */}
          <Button type="submit" onClick={() => undefined} disabled={creating}>
            {creating ? "Creating…" : "Create user"}
          </Button>
        </form>
      </div>

      {roleDialog && (
        <ConfirmDialog
          title={`Change role for ${roleDialog.user.email}?`}
          confirmLabel={busy ? "Changing…" : `Set to ${roleDialog.role}`}
          confirmDisabled={busy}
          onConfirm={confirmRoleChange}
          onCancel={() => setRoleDialog(null)}
          body={
            <>
              <p>
                {roleDialog.user.email}: {roleDialog.user.role} →{" "}
                <strong>{roleDialog.role}</strong>
              </p>
              {actionErr && (
                <p className="mt-2 text-[var(--critical)]">{actionErr}</p>
              )}
            </>
          }
        />
      )}
      {disableDialog && (
        <ConfirmDialog
          title={`Disable ${disableDialog.email}?`}
          danger
          confirmLabel={busy ? "Disabling…" : "Disable"}
          confirmDisabled={busy}
          onConfirm={confirmDisable}
          onCancel={() => setDisableDialog(null)}
          body={
            <>
              <p>
                This immediately revokes all of {disableDialog.email}&apos;s
                sessions.
              </p>
              {actionErr && (
                <p className="mt-2 text-[var(--critical)]">{actionErr}</p>
              )}
            </>
          }
        />
      )}
      {enableDialog && (
        <ConfirmDialog
          title={`Enable ${enableDialog.email}?`}
          confirmLabel={busy ? "Enabling…" : "Enable"}
          confirmDisabled={busy}
          onConfirm={confirmEnable}
          onCancel={() => setEnableDialog(null)}
          body={
            <>
              <p>{enableDialog.email} will be able to sign in again.</p>
              {actionErr && (
                <p className="mt-2 text-[var(--critical)]">{actionErr}</p>
              )}
            </>
          }
        />
      )}
      {pwDialog && (
        <ConfirmDialog
          title={`Reset password for ${pwDialog.email}?`}
          danger
          confirmLabel={busy ? "Resetting…" : "Reset password"}
          confirmDisabled={busy}
          onConfirm={confirmPwReset}
          onCancel={() => setPwDialog(null)}
          body={
            <div>
              <p className="mb-2">
                This revokes all of {pwDialog.email}&apos;s current sessions.
              </p>
              <label className="mb-1 block text-[12px]" htmlFor="reset-pw">
                New password
              </label>
              <input
                id="reset-pw"
                type="password"
                value={pwValue}
                onChange={(e) => setPwValue(e.target.value)}
                className="mb-2 w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
              />
              <p className="mb-2 text-[11px] text-[var(--text-dim)]">
                At least 12 characters.
              </p>
              <label
                className="mb-1 block text-[12px]"
                htmlFor="reset-pw-confirm"
              >
                Confirm new password
              </label>
              <input
                id="reset-pw-confirm"
                type="password"
                value={pwConfirmValue}
                onChange={(e) => setPwConfirmValue(e.target.value)}
                className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
              />
              {actionErr && (
                <p className="mt-2 text-[12px] text-[var(--critical)]">
                  {actionErr}
                </p>
              )}
            </div>
          }
        />
      )}
    </div>
  );
}

// ---- Strategy & risk (links to the full page, BL-14 lives on /strategies) --

function StrategyRiskSection() {
  const current = usePoll(() => api.config.current(), 15000);
  return (
    <Section title="Strategy & risk">
      <p className="mb-2 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Versioning, rollback, and the structured field editor live on the
        Strategies page — risk fields there require ADMIN.
      </p>
      <Await state={current} what="active config">
        {(c) => (
          <div className="flex items-center gap-3">
            <Badge tone="ok">v{c.version}</Badge>
            <span className="text-[13px] text-[var(--text-dim)]">
              created {fmtTime(c.created_at)} by {c.created_by || "system"}
            </span>
            <Link
              href="/strategies"
              className="text-[13px] text-[var(--accent)] underline"
            >
              Open Strategies →
            </Link>
          </div>
        )}
      </Await>
    </Section>
  );
}

// ---- Notifications (structured form over the same StrategyParams doc) -----

function notifRoutes(params: StrategyParams): Record<string, string[]> {
  const r = (params.notifications as Record<string, unknown>).routes;
  return r ? (JSON.parse(JSON.stringify(r)) as Record<string, string[]>) : {};
}
function notifCooldown(params: StrategyParams): string {
  const v = (params.notifications as Record<string, unknown>).cooldown_seconds;
  return v === undefined || v === null ? "" : String(v);
}

interface NotifApplyConfirm {
  params: StrategyParams;
  rows: (DiffRow & { effect: string })[];
  fromVersion: number;
}

function NotificationsSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayEdit = can(role, "scanner:config");
  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.config.current(), 15000, [refresh]);
  const [editing, setEditing] = useState(false);
  const [cooldownRaw, setCooldownRaw] = useState("");
  const [routesDraft, setRoutesDraft] = useState<Record<string, string[]>>({});
  const [cooldownErr, setCooldownErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [confirmState, setConfirmState] = useState<NotifApplyConfirm | null>(
    null,
  );
  // stale mirrors /strategies' handling of a 409 stale_version (T-058):
  // never silently re-send the draft, only Reload does.
  const [stale, setStale] = useState<{ current: number | null } | null>(null);

  const startEdit = (params: StrategyParams) => {
    setCooldownRaw(notifCooldown(params));
    setRoutesDraft(notifRoutes(params));
    setCooldownErr(null);
    setMsg(null);
    setStale(null);
    setEditing(true);
  };

  const reloadAfterStale = () => {
    setStale(null);
    setEditing(false);
    setConfirmState(null);
    setRefresh((n) => n + 1);
  };

  const toggleChannel = (
    severity: string,
    channel: string,
    enabled: boolean,
  ) => {
    setRoutesDraft((prev) => {
      const set = new Set(prev[severity] ?? []);
      if (enabled) set.add(channel);
      else set.delete(channel);
      return { ...prev, [severity]: [...set] };
    });
  };

  const review = (activeParams: StrategyParams, activeVersion: number) => {
    const err = validateCooldown(cooldownRaw);
    setCooldownErr(err);
    if (err) return;
    let params = cloneParams(activeParams);
    params = setPath(
      params,
      "notifications.cooldown_seconds",
      Number(cooldownRaw.trim()),
    );
    params = setPath(params, "notifications.routes", routesDraft);
    const rows = diffParams(
      activeParams as unknown as Record<string, unknown>,
      params as unknown as Record<string, unknown>,
    ).map((r) => ({ ...r, effect: effectFor(r.path) }));
    if (rows.length === 0) {
      setMsg({ ok: false, text: "No changes to apply." });
      return;
    }
    setConfirmState({ params, rows, fromVersion: activeVersion });
  };

  const confirmApply = async () => {
    if (!confirmState) return;
    setMsg(null);
    try {
      const snap = await api.config.apply(
        confirmState.params,
        confirmState.fromVersion,
      );
      setMsg({ ok: true, text: `Version ${snap.version} active.` });
      setStale(null);
      setEditing(false);
      setConfirmState(null);
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      if (isStaleVersion(err)) {
        setStale({ current: staleVersion(err) });
        setConfirmState(null);
        return;
      }
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Apply failed.",
      });
      setConfirmState(null);
    }
  };

  return (
    <Section title="Notifications">
      {stale && (
        <StaleVersionNotice
          currentVersion={stale.current}
          onReload={reloadAfterStale}
        />
      )}
      {msg && (
        <p
          className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
        >
          {msg.text}
        </p>
      )}
      <Await state={current} what="active config">
        {(c) => (
          <div className="max-w-xl">
            {!editing ? (
              <div className="flex items-center gap-3">
                <span className="text-[13px] text-[var(--text-dim)]">
                  cooldown {notifCooldown(c.params)}s · routes:{" "}
                  {Object.entries(notifRoutes(c.params))
                    .map(([sev, chans]) => `${sev}→${chans.join("+") || "web"}`)
                    .join(", ") || "defaults"}
                </span>
                {mayEdit && (
                  <Button onClick={() => startEdit(c.params)}>Edit</Button>
                )}
              </div>
            ) : (
              <>
                <NotificationsFields
                  cooldownRaw={cooldownRaw}
                  onCooldownChange={setCooldownRaw}
                  cooldownError={cooldownErr ?? undefined}
                  routes={routesDraft}
                  onToggleChannel={toggleChannel}
                />
                <div className="mt-3 flex gap-2">
                  <Button onClick={() => review(c.params, c.version)}>
                    Review changes
                  </Button>
                  <Button onClick={() => setEditing(false)} danger>
                    Cancel
                  </Button>
                </div>
              </>
            )}
          </div>
        )}
      </Await>
      <TelegramAllowlistSection />
      {confirmState && (
        <ConfirmDialog
          title="Apply new notification settings?"
          confirmLabel="Apply new version"
          onConfirm={confirmApply}
          onCancel={() => setConfirmState(null)}
          body={
            <>
              <p className="mb-3">
                This becomes a new strategy config version on top of v
                {confirmState.fromVersion} — it&apos;s the same versioned
                document Strategies edits, so it shows there too.
              </p>
              <DiffTable
                rows={confirmState.rows}
                beforeLabel={`Current (v${confirmState.fromVersion})`}
                afterLabel="New (draft)"
                showEffect
              />
            </>
          }
        />
      )}
    </Section>
  );
}

export default function SettingsPage() {
  return (
    <ConsoleShell active="Settings">
      <PageTitle>Settings</PageTitle>
      <Section title="Setup wizard">
        <p className="max-w-2xl text-[13px] text-[var(--text-dim)]">
          Pick venues, set simulated paper balances and create a rule in three
          short steps.{" "}
          <Link href="/onboarding" className="text-[var(--accent)] underline">
            Run the setup wizard →
          </Link>
        </p>
      </Section>
      <SessionSection />
      <div id="operating-mode">
        <OperatingModeSection />
      </div>
      <div id="markets">
        <MarketsSection />
      </div>
      <VenuesSection />
      <div id="scanner-suite">
        <ScreenerSettingsSection />
      </div>
      <div id="logging">
        <LoggingAccessSection />
      </div>
      <div id="ai">
        <AIAdvisorSection />
      </div>
      <div id="platform-versions">
        <PlatformVersionHistorySection />
      </div>
      <div id="users">
        <UsersSection />
      </div>
      <StrategyRiskSection />
      <div id="notifications">
        <NotificationsSection />
      </div>
      <div id="security">
        <SecretsSection />
      </div>
      <Section title="Security posture">
        <ul className="max-w-2xl list-inside list-disc space-y-1 text-[13px] text-[var(--text-dim)]">
          <li>
            Live trading is permanently disabled by design (LiveExecutor returns
            ErrLiveTradingDisabled).
          </li>
          <li>
            Sessions are server-side and revocable; CSRF required on every state
            change; RBAC enforced in the backend.
          </li>
          <li>
            Exchange access is public market data only — no API keys with trade,
            withdrawal, or transfer permissions exist anywhere in this system.
          </li>
          <li>
            MFA (TOTP) enrollment is reserved in the auth flow but not yet
            implemented (MASTER_PLAN T-052).
          </li>
        </ul>
      </Section>
    </ConsoleShell>
  );
}
