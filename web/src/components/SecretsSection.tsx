"use client";

// Settings → Security — the write-only secrets vault (T-060,
// docs/design/settings-expansion.md §3/§6). Values are NEVER returned by
// the backend and this component never renders one back: the "Set value"
// input is type=password, autocomplete=off, and is cleared the instant a
// submit starts (success or failure) rather than only on success — the
// operator retypes on a retry, the assistant never sees or handles a
// value.

import { useState } from "react";
import { api, ApiError, type SecretInfo } from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { Await, Badge, Button, ConfirmDialog, Section, Table, fmtTime } from "@/components/ui";

function appliesLabel(applies: string): string {
  return applies === "immediately" ? "Immediately" : applies === "process_restart" ? "On process restart" : applies;
}

export function SecretsSection() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayEdit = can(role, "system:config");

  const [refresh, setRefresh] = useState(0);
  const list = usePoll(() => api.secrets.list(), 15000, [refresh]);

  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [setDialog, setSetDialog] = useState<SecretInfo | null>(null);
  const [value, setValue] = useState("");
  const [removeDialog, setRemoveDialog] = useState<SecretInfo | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const openSet = (s: SecretInfo) => {
    setValue("");
    setErr("");
    setSetDialog(s);
  };
  const closeSet = () => {
    setValue(""); // never kept around, success or cancel
    setSetDialog(null);
  };
  const confirmSet = async () => {
    if (!setDialog) return;
    const v = value;
    setValue("");
    setBusy(true);
    setErr("");
    try {
      const info = await api.secrets.put(setDialog.name, v);
      setMsg({ ok: true, text: `${info.label} saved. Takes effect: ${appliesLabel(info.applies)}.` });
      setSetDialog(null);
      setRefresh((n) => n + 1);
    } catch (e: unknown) {
      setErr(e instanceof ApiError ? e.message : "Save failed.");
    } finally {
      setBusy(false);
    }
  };

  const confirmRemove = async () => {
    if (!removeDialog) return;
    setBusy(true);
    setErr("");
    try {
      const info = await api.secrets.delete(removeDialog.name);
      setMsg({ ok: true, text: `${info.label} removed; falls back to the environment if one is set there.` });
      setRemoveDialog(null);
      setRefresh((n) => n + 1);
    } catch (e: unknown) {
      setErr(e instanceof ApiError ? e.message : "Remove failed.");
      setRemoveDialog(null);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section title="Security">
      {msg && <p className={`mb-2 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}>{msg.text}</p>}
      <Await state={list} what="secrets">
        {(l) => (
          <div className="max-w-3xl space-y-3">
            <p className="text-[13px] text-[var(--text-dim)]">
              {l.vault_configured ? (
                <>
                  Vault configured{l.key_id ? ` (key ${l.key_id})` : ""}.
                </>
              ) : (
                <span className="text-[var(--warn)]">
                  Vault not configured{l.reason ? ` — ${l.reason}` : ""}. Env-provided secrets still work; writes
                  here are unavailable until it is.
                </span>
              )}
            </p>
            <Table
              head={["Secret", "Status", "Source", "Updated", "Applies", ""]}
              empty="registered secrets"
              rows={(l.secrets ?? []).map((s) => [
                s.label,
                s.present ? (
                  s.readable ? (
                    <Badge key="p" tone="ok">
                      Present
                    </Badge>
                  ) : (
                    <Badge key="p" tone="warn">
                      Present, unreadable
                    </Badge>
                  )
                ) : (
                  <Badge key="p" tone="dim">
                    Not set
                  </Badge>
                ),
                s.source ?? "—",
                s.updated_at ? `${fmtTime(s.updated_at)}${s.updated_by ? ` by ${s.updated_by}` : ""}` : "—",
                appliesLabel(s.applies),
                mayEdit ? (
                  <div key="actions" className="flex gap-1.5">
                    <Button onClick={() => openSet(s)} disabled={!l.vault_configured}>
                      Set value
                    </Button>
                    <Button onClick={() => setRemoveDialog(s)} disabled={!l.vault_configured || !s.present} danger>
                      Remove
                    </Button>
                  </div>
                ) : (
                  ""
                ),
              ])}
            />
            {(l.secrets ?? []).some((s) => s.reason) && (
              <ul className="list-inside list-disc text-[12px] text-[var(--warn)]">
                {(l.secrets ?? [])
                  .filter((s) => s.reason)
                  .map((s) => (
                    <li key={s.name}>
                      {s.label}: {s.reason}
                    </li>
                  ))}
              </ul>
            )}
            {!mayEdit && <p className="text-[12px] text-[var(--text-dim)]">Setting or removing requires ADMIN (system:config).</p>}
          </div>
        )}
      </Await>

      {setDialog && (
        <ConfirmDialog
          title={`Set ${setDialog.label}?`}
          confirmLabel={busy ? "Saving…" : "Save"}
          confirmDisabled={busy || value.length === 0}
          onConfirm={confirmSet}
          onCancel={closeSet}
          body={
            <div>
              <p className="mb-2">
                This overwrites the current value. It is never shown or logged anywhere — including here
                after you save it. Applies: {appliesLabel(setDialog.applies)}.
              </p>
              <label className="mb-1 block text-[12px]" htmlFor="secret-value">
                New value
              </label>
              <input
                id="secret-value"
                type="password"
                autoComplete="off"
                autoFocus
                value={value}
                onChange={(e) => setValue(e.target.value)}
                className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
              />
              {err && <p className="mt-2 text-[var(--critical)]">{err}</p>}
            </div>
          }
        />
      )}
      {removeDialog && (
        <ConfirmDialog
          title={`Remove ${removeDialog.label}?`}
          danger
          confirmLabel={busy ? "Removing…" : "Remove"}
          confirmDisabled={busy}
          onConfirm={confirmRemove}
          onCancel={() => setRemoveDialog(null)}
          body={
            <div>
              <p>
                This deletes the stored value; the secret falls back to its environment variable, if any is
                set there. Applies: {appliesLabel(removeDialog.applies)}.
              </p>
              {err && <p className="mt-2 text-[var(--critical)]">{err}</p>}
            </div>
          }
        />
      )}
    </Section>
  );
}
