"use client";

import { useState } from "react";
import { api, ApiError, isStaleVersion, staleVersion, type StrategyParams } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { diffParams, effectFor, fmtDiffValue, type DiffRow } from "@/lib/diff";
import {
  ALL_FIELDS,
  RISK_FIELDS,
  SCANNER_FIELDS,
  cloneParams,
  getPath,
  setPath,
  validateCooldown,
  validateField,
  fieldPathFromError,
  type FieldSpec,
} from "@/lib/strategyFields";
import { ConsoleShell } from "@/components/ConsoleShell";
import { NotificationsFields } from "@/components/NotificationsFields";
import {
  Await,
  Badge,
  Button,
  ConfirmDialog,
  DiffTable,
  PageTitle,
  Section,
  StaleVersionNotice,
  Table,
  fmtTime,
} from "@/components/ui";

interface ApplyConfirm {
  params: StrategyParams;
  rows: (DiffRow & { effect: string })[];
  fromVersion: number;
}

interface RollbackConfirm {
  version: number;
  rows: DiffRow[];
  fromVersion: number;
}

function notifRoutes(params: StrategyParams): Record<string, string[]> {
  const r = (params.notifications as Record<string, unknown>).routes;
  return r ? (JSON.parse(JSON.stringify(r)) as Record<string, string[]>) : {};
}

function notifCooldown(params: StrategyParams): string {
  const v = (params.notifications as Record<string, unknown>).cooldown_seconds;
  return v === undefined || v === null ? "" : String(v);
}

function FieldRow({
  spec,
  value,
  error,
  disabled,
  disabledNote,
  onChange,
}: {
  spec: FieldSpec;
  value: string;
  error?: string;
  disabled?: boolean;
  disabledNote?: string;
  onChange: (v: string) => void;
}) {
  const id = `field-${spec.path}`;
  return (
    <div>
      <label className="mb-1 block text-[12px] text-[var(--text-dim)]" htmlFor={id}>
        {spec.label}
        {disabled && disabledNote && (
          <span className="ml-1 text-[var(--warn)]">({disabledNote})</span>
        )}
      </label>
      <input
        id={id}
        type="text"
        inputMode={spec.kind === "int" ? "numeric" : "decimal"}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        className={`w-full rounded border bg-[var(--bg)] px-2 py-1 text-[13px] outline-none disabled:opacity-50 ${
          error ? "border-[var(--critical)]" : "border-[var(--border)] focus:border-[var(--accent)]"
        }`}
      />
      <p className="mt-1 text-[11px] text-[var(--text-dim)]">
        {spec.help}
        {spec.effect === "on restart" && (
          <span className="ml-1 text-[var(--warn)]">Applies on restart, not hot-swapped.</span>
        )}
      </p>
      {error && <p className="mt-1 text-[11px] text-[var(--critical)]">{error}</p>}
    </div>
  );
}

export default function StrategiesPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const [refresh, setRefresh] = useState(0);
  const current = usePoll(() => api.config.current(), 10000, [refresh]);
  const versions = usePoll(() => api.config.versions(25), 10000, [refresh]);

  const [editing, setEditing] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [fieldRaw, setFieldRaw] = useState<Record<string, string>>({});
  const [cooldownRaw, setCooldownRaw] = useState("");
  const [routesDraft, setRoutesDraft] = useState<Record<string, string[]>>({});
  const [jsonText, setJsonText] = useState("");
  // jsonBaseVersion (T-058): the version jsonText was actually seeded
  // from (startEdit or the last switchToAdvanced) — NOT necessarily the
  // live activeVersion, which usePoll can advance while the Advanced:
  // JSON draft sits open and unrefreshed. The structured form doesn't
  // need this: buildFromFields re-clones the live activeParams on every
  // review, so activeVersion genuinely is that payload's parent.
  const [jsonBaseVersion, setJsonBaseVersion] = useState<number | null>(null);
  const [jsonError, setJsonError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [applyConfirm, setApplyConfirm] = useState<ApplyConfirm | null>(null);
  const [rollbackConfirm, setRollbackConfirm] = useState<RollbackConfirm | null>(null);
  const [rollbackLoading, setRollbackLoading] = useState<number | null>(null);
  const [rollbackErr, setRollbackErr] = useState("");
  // stale is non-null once a 409 stale_version comes back from apply or
  // rollback (T-058) — the draft is never silently re-sent against the
  // new version; the operator must explicitly Reload. `current` is the
  // version that won the race (null if the backend didn't send one).
  const [stale, setStale] = useState<{ current: number | null } | null>(null);

  const activeParams = current.kind === "ready" ? current.data.params : null;
  const activeVersion = current.kind === "ready" ? current.data.version : null;
  const mayEdit = can(role, "scanner:config");
  const mayEditRisk = role === "ADMIN";

  const populateFromParams = (params: StrategyParams) => {
    const raw: Record<string, string> = {};
    for (const f of ALL_FIELDS) {
      const v = getPath(params, f.path);
      raw[f.path] = v === undefined || v === null ? "" : String(v);
    }
    setFieldRaw(raw);
    setCooldownRaw(notifCooldown(params));
    setRoutesDraft(notifRoutes(params));
    setFieldErrors({});
  };

  const startEdit = () => {
    if (!activeParams || activeVersion === null) return;
    setMsg(null);
    setJsonError(null);
    setStale(null);
    populateFromParams(activeParams);
    setJsonText(JSON.stringify(activeParams, null, 2));
    setJsonBaseVersion(activeVersion);
    setAdvanced(false);
    setEditing(true);
  };

  // reloadAfterStale (T-058): the operator's only way forward after a 409
  // — discard the stale draft and refetch the now-current version. Never
  // triggered automatically; the banner's Reload button is the one path
  // here that re-sends anything.
  const reloadAfterStale = () => {
    setStale(null);
    discard();
    setApplyConfirm(null);
    setRollbackConfirm(null);
    setRefresh((n) => n + 1);
  };

  const discard = () => {
    setEditing(false);
    setAdvanced(false);
    setFieldErrors({});
    setJsonError(null);
    setJsonBaseVersion(null);
  };

  // buildFromFields folds the current per-field raw text into a full
  // params document by cloning the active version and overwriting only
  // the leaves the operator touched (never re-serializing untouched
  // fields, per §4.4 — avoids manufacturing a diff on fields the
  // operator didn't edit and possibly tripping the risk->ADMIN gate).
  const buildFromFields = (base: StrategyParams): { params: StrategyParams; errors: Record<string, string> } => {
    let params = cloneParams(base);
    const errors: Record<string, string> = {};
    for (const f of ALL_FIELDS) {
      if (f.section === "risk" && !mayEditRisk) continue; // untouchable, leave as-is
      const raw = fieldRaw[f.path] ?? "";
      const err = validateField(f, raw);
      if (err) {
        errors[f.path] = err;
        continue;
      }
      const value = f.kind === "decimal" ? raw.trim() : Number(raw.trim());
      params = setPath(params, f.path, value);
    }
    const cdErr = validateCooldown(cooldownRaw);
    if (cdErr) {
      errors["notifications.cooldown_seconds"] = cdErr;
    } else {
      params = setPath(params, "notifications.cooldown_seconds", Number(cooldownRaw.trim()));
    }
    params = setPath(params, "notifications.routes", routesDraft);
    return { params, errors };
  };

  const switchToAdvanced = () => {
    if (!activeParams || activeVersion === null) return;
    const { params, errors } = buildFromFields(activeParams);
    // buildFromFields silently keeps the last-valid value for any field
    // that fails validation — refuse the switch rather than seed the JSON
    // view with an edit the operator just typed and would otherwise lose
    // without any message.
    if (Object.keys(errors).length > 0) {
      setFieldErrors(errors);
      setMsg({ ok: false, text: "Fix the highlighted fields before switching to Advanced: JSON." });
      return;
    }
    setJsonText(JSON.stringify(params, null, 2));
    // The JSON draft is reseeded from the live activeParams right here —
    // that's the version it's now based on, until the next reseed.
    setJsonBaseVersion(activeVersion);
    setJsonError(null);
    setAdvanced(true);
  };

  const switchToStructured = () => {
    try {
      const parsed = JSON.parse(jsonText) as StrategyParams;
      populateFromParams(parsed);
      setAdvanced(false);
      setJsonError(null);
    } catch {
      setJsonError("Draft is not valid JSON — fix it before switching back to the structured form.");
    }
  };

  const toggleChannel = (severity: string, channel: string, enabled: boolean) => {
    setRoutesDraft((prev) => {
      const current = new Set(prev[severity] ?? []);
      if (enabled) current.add(channel);
      else current.delete(channel);
      return { ...prev, [severity]: [...current] };
    });
  };

  const reviewChanges = () => {
    if (!activeParams || activeVersion === null) return;
    // parent_version (T-058): the structured form's payload is always
    // built fresh off the live activeParams below, so activeVersion IS
    // that payload's parent. The Advanced: JSON draft, by contrast, sits
    // in a textarea the operator can leave open past the next poll tick
    // — its parent is whatever version it was last seeded from
    // (jsonBaseVersion), not necessarily today's activeVersion.
    const fromVersion = advanced ? jsonBaseVersion : activeVersion;
    if (fromVersion === null) return;
    setMsg(null);
    let params: StrategyParams;
    let errors: Record<string, string> = {};
    if (advanced) {
      try {
        params = JSON.parse(jsonText) as StrategyParams;
        setJsonError(null);
      } catch {
        setJsonError("Draft is not valid JSON.");
        return;
      }
    } else {
      const built = buildFromFields(activeParams);
      params = built.params;
      errors = built.errors;
    }
    setFieldErrors(errors);
    if (Object.keys(errors).length > 0) {
      setMsg({ ok: false, text: "Fix the highlighted fields before reviewing changes." });
      return;
    }
    const rows = diffParams(
      activeParams as unknown as Record<string, unknown>,
      params as unknown as Record<string, unknown>,
    ).map((r) => ({ ...r, effect: effectFor(r.path) }));
    if (rows.length === 0) {
      setMsg({ ok: false, text: "No changes to apply." });
      return;
    }
    setApplyConfirm({ params, rows, fromVersion });
  };

  const confirmApply = async () => {
    if (!applyConfirm) return;
    setMsg(null);
    try {
      const snap = await api.config.apply(applyConfirm.params, applyConfirm.fromVersion);
      setMsg({ ok: true, text: `Version ${snap.version} active.` });
      setStale(null);
      setApplyConfirm(null);
      discard();
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      if (isStaleVersion(err)) {
        setStale({ current: staleVersion(err) });
        setApplyConfirm(null);
        return;
      }
      const text = err instanceof ApiError ? err.message : "Apply failed.";
      const path = err instanceof ApiError ? fieldPathFromError(text) : null;
      if (path && !advanced) {
        setFieldErrors((prev) => ({ ...prev, [path]: text }));
        setMsg({ ok: false, text: "The backend rejected this change — see the highlighted field." });
      } else {
        setMsg({ ok: false, text });
      }
      setApplyConfirm(null);
    }
  };

  const reviewRollback = async (version: number) => {
    if (activeParams === null || activeVersion === null) return;
    setRollbackErr("");
    setRollbackLoading(version);
    try {
      const target = await api.config.version(version);
      const rows = diffParams(
        activeParams as unknown as Record<string, unknown>,
        target.params as unknown as Record<string, unknown>,
      );
      setRollbackConfirm({ version, rows, fromVersion: activeVersion });
    } catch (err: unknown) {
      setRollbackErr(err instanceof ApiError ? err.message : "Could not load that version.");
    } finally {
      setRollbackLoading(null);
    }
  };

  const confirmRollback = async () => {
    if (!rollbackConfirm) return;
    setMsg(null);
    try {
      const snap = await api.config.rollback(rollbackConfirm.version, rollbackConfirm.fromVersion);
      setMsg({ ok: true, text: `Rolled back as new version ${snap.version}.` });
      setStale(null);
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      if (isStaleVersion(err)) {
        setStale({ current: staleVersion(err) });
        return;
      }
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Rollback failed." });
    } finally {
      setRollbackConfirm(null);
    }
  };

  return (
    <ConsoleShell active="Strategies">
      <PageTitle>Strategy Configuration</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Every change becomes an immutable version with a diff, actor, and audit event, and hot-swaps
        into the running scanner — except <strong>scanner.workers</strong>, which only applies the next
        time the engine process starts. Risk-section changes require ADMIN; the backend validates bounds.
      </p>
      {stale && <StaleVersionNotice currentVersion={stale.current} onReload={reloadAfterStale} />}
      {msg && (
        <p className={`mb-3 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>
      )}
      <Section title="Active version">
        <Await state={current} what="active config">
          {(c) => (
            <>
              <div className="mb-3 flex items-center gap-3 text-[13px]">
                <Badge tone="ok">v{c.version}</Badge>
                <span className="text-[var(--text-dim)]">
                  created {fmtTime(c.created_at)} by {c.created_by || "system"}
                  {c.parent_version ? ` (parent v${c.parent_version})` : ""}
                </span>
                {mayEdit && !editing && <Button onClick={startEdit}>Edit configuration</Button>}
                {editing && (
                  <>
                    <Button onClick={advanced ? switchToStructured : switchToAdvanced}>
                      {advanced ? "Structured form" : "Advanced: JSON"}
                    </Button>
                    <Button onClick={discard} danger>
                      Discard draft
                    </Button>
                  </>
                )}
              </div>

              {!editing ? (
                <pre className="max-h-96 overflow-auto rounded border border-[var(--border)] bg-[var(--bg-panel)] p-3 text-[12px]">
                  {JSON.stringify(c.params, null, 2)}
                </pre>
              ) : advanced ? (
                <>
                  {jsonError && <p className="mb-2 text-[12px] text-[var(--critical)]">{jsonError}</p>}
                  <textarea
                    value={jsonText}
                    onChange={(e) => setJsonText(e.target.value)}
                    spellCheck={false}
                    className="h-96 w-full rounded border border-[var(--accent)] bg-[var(--bg-panel)] p-3 font-mono text-[12px] outline-none"
                  />
                  <div className="mt-2 flex gap-2">
                    <Button onClick={reviewChanges}>Review changes</Button>
                  </div>
                </>
              ) : (
                <div className="space-y-6">
                  <div>
                    <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
                      Scanner
                    </h3>
                    <div className="grid max-w-3xl grid-cols-1 gap-4 sm:grid-cols-2">
                      {SCANNER_FIELDS.map((f) => (
                        <FieldRow
                          key={f.path}
                          spec={f}
                          value={fieldRaw[f.path] ?? ""}
                          error={fieldErrors[f.path]}
                          onChange={(v) => setFieldRaw((prev) => ({ ...prev, [f.path]: v }))}
                        />
                      ))}
                    </div>
                  </div>
                  <div>
                    <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
                      Risk
                    </h3>
                    <div className="grid max-w-3xl grid-cols-1 gap-4 sm:grid-cols-2">
                      {RISK_FIELDS.map((f) => (
                        <FieldRow
                          key={f.path}
                          spec={f}
                          value={fieldRaw[f.path] ?? ""}
                          error={fieldErrors[f.path]}
                          disabled={!mayEditRisk}
                          disabledNote={!mayEditRisk ? "requires ADMIN" : undefined}
                          onChange={(v) => setFieldRaw((prev) => ({ ...prev, [f.path]: v }))}
                        />
                      ))}
                    </div>
                  </div>
                  <div>
                    <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wider text-[var(--text-dim)]">
                      Notifications
                    </h3>
                    <NotificationsFields
                      cooldownRaw={cooldownRaw}
                      onCooldownChange={setCooldownRaw}
                      cooldownError={fieldErrors["notifications.cooldown_seconds"]}
                      routes={routesDraft}
                      onToggleChannel={toggleChannel}
                    />
                  </div>
                  <Button onClick={reviewChanges}>Review changes</Button>
                </div>
              )}
            </>
          )}
        </Await>
      </Section>
      <Section title="Version history (rollback creates a new version)">
        {rollbackErr && <p className="mb-2 text-[12px] text-[var(--critical)]">{rollbackErr}</p>}
        <Await state={versions} what="config versions">
          {(list) => (
            <Table
              head={["Version", "Created", "By", "Parent", "Changed paths", ""]}
              empty="versions"
              rows={(list ?? []).map((v) => [
                v.active ? <Badge key="a" tone="ok">v{v.version} active</Badge> : `v${v.version}`,
                fmtTime(v.created_at),
                v.created_by || "system",
                v.parent_version ? `v${v.parent_version}` : "—",
                v.diff && Object.keys(v.diff).length > 0 ? (
                  <div key="d" className="max-w-xs whitespace-normal break-words text-[12px]">
                    {Object.entries(v.diff).map(([path, change]) => (
                      <div key={path}>
                        <span className="text-[var(--text-dim)]">{path}:</span>{" "}
                        {fmtDiffValue(change.old)} → {fmtDiffValue(change.new)}
                      </div>
                    ))}
                  </div>
                ) : (
                  "—"
                ),
                !v.active && mayEdit ? (
                  <Button
                    key="rb"
                    onClick={() => reviewRollback(v.version)}
                    disabled={rollbackLoading === v.version}
                  >
                    {rollbackLoading === v.version ? "Loading…" : "Roll back to"}
                  </Button>
                ) : (
                  ""
                ),
              ])}
            />
          )}
        </Await>
      </Section>

      {applyConfirm && (
        <ConfirmDialog
          title="Apply new strategy configuration?"
          confirmLabel="Apply new version"
          onConfirm={confirmApply}
          onCancel={() => setApplyConfirm(null)}
          body={
            <>
              <p className="mb-3">
                This becomes a new version on top of v{applyConfirm.fromVersion}. Fields marked{" "}
                <strong>immediate</strong> take effect in the running scanner as soon as you confirm;
                fields marked <strong>on restart</strong> are saved now but only take effect the next
                time the engine process starts.
              </p>
              <DiffTable
                rows={applyConfirm.rows}
                beforeLabel={`Current (v${applyConfirm.fromVersion})`}
                afterLabel="New (draft)"
                showEffect
              />
            </>
          }
        />
      )}

      {rollbackConfirm && (
        <ConfirmDialog
          title={`Roll back to v${rollbackConfirm.version}?`}
          confirmLabel={`Roll back to v${rollbackConfirm.version}`}
          onConfirm={confirmRollback}
          onCancel={() => setRollbackConfirm(null)}
          body={
            <>
              <p className="mb-3">
                This creates a new version with v{rollbackConfirm.version}&apos;s parameters. Versions
                between v{rollbackConfirm.version} and v{rollbackConfirm.fromVersion} remain in history
                and can be rolled back to again later.
              </p>
              <DiffTable
                rows={rollbackConfirm.rows}
                beforeLabel={`Current (v${rollbackConfirm.fromVersion})`}
                afterLabel={`Restoring (v${rollbackConfirm.version})`}
              />
            </>
          }
        />
      )}
    </ConsoleShell>
  );
}
