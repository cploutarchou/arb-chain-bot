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
import {
  SettingsAnchor,
  SettingsCategories,
  type SettingsCategoryDef,
} from "@/components/SettingsCategories";
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
            label="Platform users"
            rowKeys={list.map((u) => u.id)}
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
  const platformAdmin =
    auth.kind === "authenticated" && auth.me.platform_admin === true;
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
      {/* TelegramAllowlistSection polls GET /platform/settings, which
          internal/api/platformapi.go wraps in requirePlatformAdmin — so
          for a VIEWER, an OPERATOR or a tenant ADMIN it rendered an
          ErrorBox carrying `platform_admin_required` inside a category
          every member sees. It is a platform-wide destination allowlist,
          not a personal notification preference, so it is gated like the
          rest of the platform configuration rather than explained. */}
      {platformAdmin && <TelegramAllowlistSection />}
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
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  // platform_admin, never the ADMIN display role. A tenant's console
  // role can equal ADMIN while `platform_admin` is false; the backend
  // refuses every platform-settings route in that case with
  // `platform_admin_required` (internal/api/auth.go:535-548), and until
  // now the console showed those forms anyway. Hiding them is the
  // presentation catching up with a gate that already exists — nothing
  // is being loosened, and nothing a tenant admin could previously
  // *change* becomes unavailable.
  const platformAdmin =
    auth.kind === "authenticated" && auth.me.platform_admin === true;
  // Scanner Suite settings and Strategy & risk are a different tier:
  // the backend gates them on the global role rather than on
  // platform_admin — PermScreenerConfig and PermRiskConfig, both ADMIN
  // only (internal/auth/rbac.go:59-67). Note this is `screener:config`,
  // the Scanner Suite mutation permission, NOT `scanner:config`, which
  // gates triangular *strategy* config and which OPERATOR also holds
  // (rbac.go:37-39 spells the two apart). Using the latter here would
  // show an OPERATOR forms the backend then refuses.
  const mayConfigure =
    can(role, "screener:config") || can(role, "risk:config");
  // Reading Scanner Suite settings is a *different* permission from
  // changing them: GET /api/v1/screener/settings is registered with
  // PermScreenerView, which internal/auth/rbac.go grants to VIEWER,
  // OPERATOR and ADMIN alike (rbac.go:49,56,65). Master rendered the
  // panel unconditionally and let it self-gate — every input carries
  // `disabled={!editing}` and the Edit button is behind its own
  // `mayEdit` — so a non-ADMIN saw a complete read-only view of poll
  // interval, liquidity floor, per-venue enablement and paper balances.
  // Gating the whole category on the *write* permission took that away
  // from exactly the roles whose job is to watch it.
  const mayReadScanner = can(role, "screener:view");

  const categories: SettingsCategoryDef[] = [
    {
      id: "account",
      label: "Account",
      description: "Your sign-in, your password and this session.",
      content: (
        <>
          <SettingsAnchor anchor="account">
            <SessionSection />
          </SettingsAnchor>
          <Section title="Getting set up">
            <p className="max-w-2xl text-[13px] text-[var(--text-dim)]">
              Pick venues, set simulated paper balances and create a rule in
              three short steps.{" "}
              <Link href="/onboarding" className="text-[var(--accent)] underline">
                Run the setup wizard →
              </Link>
            </p>
          </Section>
          <Section title="How this platform keeps you safe">
            {/* Plain language, same guarantees. The previous copy named
                LiveExecutor, ErrLiveTradingDisabled, CSRF and a task id,
                which told an ordinary reader nothing about what is
                actually protected. The engineering detail lives in
                docs/security.md, not in an account settings page. */}
            <ul className="max-w-2xl list-inside list-disc space-y-1 text-[13px] text-[var(--text-dim)]">
              <li>
                This platform never places a real order. Live trading is
                disabled in the code itself, not by a setting anyone can
                switch.
              </li>
              <li>
                Exchange access is read-only public market data. No key with
                permission to trade, withdraw or transfer exists anywhere in
                this system.
              </li>
              <li>
                Your session lives on the server and can be revoked, and every
                change you make is checked against your role by the backend —
                not merely hidden in the interface.
              </li>
              <li>
                Two-factor sign-in is planned but not yet available.
              </li>
            </ul>
          </Section>
        </>
      ),
    },
    {
      id: "notifications",
      label: "Notifications",
      description: "How and when this platform contacts you.",
      content: (
        <SettingsAnchor anchor="notifications">
          <NotificationsSection />
        </SettingsAnchor>
      ),
    },
  ];

  // Administration appears for anyone who can read or configure
  // something in it — not only for someone who can write.
  if (mayReadScanner || mayConfigure || platformAdmin) {
    categories.push({
      id: "administration",
      label: "Administration",
      description: platformAdmin
        ? "Platform-wide configuration. Changes here affect every organisation on this deployment, and most apply on the next engine restart."
        : mayConfigure
          ? "Trading configuration for this deployment. Platform-wide settings are operated by platform staff and are not shown here."
          : "The scanning configuration this deployment runs on, read-only for your role. Changing it needs an administrator.",
      content: (
        <>
          {mayReadScanner && (
            <SettingsAnchor anchor="scanner-suite">
              <ScreenerSettingsSection />
            </SettingsAnchor>
          )}
          {mayConfigure && (
            <SettingsAnchor anchor="strategy-risk">
              <StrategyRiskSection />
            </SettingsAnchor>
          )}
          {platformAdmin ? (
            <>
              <SettingsAnchor anchor="operating-mode">
                <OperatingModeSection />
              </SettingsAnchor>
              <SettingsAnchor anchor="markets">
                <MarketsSection />
              </SettingsAnchor>
              <SettingsAnchor anchor="venues">
                <VenuesSection />
              </SettingsAnchor>
              <SettingsAnchor anchor="ai">
                <AIAdvisorSection />
              </SettingsAnchor>
              <SettingsAnchor anchor="logging">
                <LoggingAccessSection />
              </SettingsAnchor>
              <SettingsAnchor anchor="users">
                <UsersSection />
              </SettingsAnchor>
              <SettingsAnchor anchor="security">
                <SecretsSection />
              </SettingsAnchor>
              <SettingsAnchor anchor="platform-versions">
                <PlatformVersionHistorySection />
              </SettingsAnchor>
            </>
          ) : (
            <Section title="Platform configuration">
              <p className="max-w-2xl text-[13px] text-[var(--text-dim)]">
                Operating mode, markets and assets, venues and fees, AI
                settings, logging, platform users and the credential vault are
                operated by platform staff for the whole deployment. Your
                organisation&apos;s own members and roles are under{" "}
                <Link href="/org" className="text-[var(--accent)] underline">
                  Organisation
                </Link>
                .
              </p>
            </Section>
          )}
        </>
      ),
    });
  }

  return (
    <ConsoleShell>
      <PageTitle>Settings</PageTitle>
      {/* ready: while the session is loading the category list is
          legitimately short, and an anchor for a category that has not
          appeared yet must not be reported as one this role cannot open. */}
      <SettingsCategories
        categories={categories}
        ready={auth.kind !== "loading"}
      />
    </ConsoleShell>
  );
}

// Focused categories (client-area audit §7 / refine command §4F): the
// former single mixed-purpose form becomes Account, Organisation,
// Notifications and — for entitled operator staff only —
// Administration. Inactive categories stay MOUNTED but hidden: the
// heavy platform editors keep their unsaved drafts and poll state, so
// switching a tab never discards an edit, and a deep link
// (/settings#markets, #users, #security, #scanner-suite,
// #operating-mode, #logging, #ai, #platform-versions, #notifications)
// activates the right category and focuses its anchor.

