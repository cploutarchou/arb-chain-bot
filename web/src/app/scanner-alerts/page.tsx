"use client";

// Alert Rules (design §5/§7: GET/POST/PUT/DELETE /screener/rules, GET
// /screener/events). Mutations (create/edit/delete/enable/disable) need
// screener:config (ADMIN) + CSRF + parent_version-free versioning (§7
// rules are not a versioned document, unlike settings) — VIEWER/OPERATOR
// get the read-only table and event history.

import { useState } from "react";
import {
  api,
  ApiError,
  type ScreenerRule,
  type ScreenerRuleInput,
  type ScreenerRuleKind,
  type ScreenerRuleParams,
} from "@/lib/api/client";
import { usePoll } from "@/lib/usePoll";
import { useAuth, can } from "@/lib/auth";
import { ConsoleShell } from "@/components/ConsoleShell";
import {
  ScreenerAwait,
  VenueChips,
  fractionToPercentStr,
  parseCsv,
  percentToFractionStr,
  signedText,
} from "@/components/screener/ScreenerShared";
import {
  Badge,
  Button,
  ConfirmDialog,
  PageTitle,
  Section,
  Table,
  fmtTime,
} from "@/components/ui";

const KINDS: ScreenerRuleKind[] = ["spread", "carry", "basis"];

function RuleForm({
  initial,
  busy,
  error,
  onCancel,
  onSave,
}: {
  initial: ScreenerRule | null;
  busy: boolean;
  error: string;
  onCancel: () => void;
  onSave: (input: ScreenerRuleInput) => void;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [kind, setKind] = useState<ScreenerRuleKind>(initial?.kind ?? "spread");
  const [minSpreadBps, setMinSpreadBps] = useState(
    initial?.min_spread_bps ?? "",
  );
  // min_carry_apr is a FRACTION on the wire (internal/screener/basis.go
  // MinCarryAPR / signals.go CarryAPR — e.g. 0.10 for 10% APR) but the
  // console shows and accepts a PERCENT, same convention as the
  // Perpetuals page's min_carry_apr filter. The conversion happens once
  // here, at the form's load boundary, and once more at submit() below —
  // never inside api/client.ts or on every keystroke, so an unrelated
  // caller (e.g. toggleEnabled's PUT of the untouched rule) never
  // double-converts an already-fraction value.
  const [minCarryApr, setMinCarryApr] = useState(
    initial?.min_carry_apr ? fractionToPercentStr(initial.min_carry_apr) : "",
  );
  const [minLiquidityQuote, setMinLiquidityQuote] = useState(
    initial?.min_liquidity_quote ?? "0",
  );
  const [minLifetimeS, setMinLifetimeS] = useState(
    String(initial?.min_lifetime_s ?? 0),
  );
  const [buyVenues, setBuyVenues] = useState<string[]>(
    initial?.buy_venues ?? [],
  );
  const [sellVenues, setSellVenues] = useState<string[]>(
    initial?.sell_venues ?? [],
  );
  const [quotesText, setQuotesText] = useState(
    (initial?.quotes ?? ["USDT"]).join(", "),
  );
  const [basesAllowText, setBasesAllowText] = useState(
    (initial?.bases_allow ?? []).join(", "),
  );
  const [basesDenyText, setBasesDenyText] = useState(
    (initial?.bases_deny ?? []).join(", "),
  );
  const [cooldownS, setCooldownS] = useState(
    String(initial?.cooldown_s ?? 300),
  );
  const [telegram, setTelegram] = useState(initial?.telegram ?? true);
  const [autoPaper, setAutoPaper] = useState(initial?.auto_paper ?? false);
  const [paperSizeQuote, setPaperSizeQuote] = useState(
    initial?.paper_size_quote ?? "100",
  );

  // Advanced (strategy-model inputs, rule_params.go): only the four the
  // task calls out — mmr is the load-bearing one (§3.4: without it every
  // carry/basis rule skips every entry with MMR_UNKNOWN), slip_bps /
  // buffer_bps / max_hold_h are the other commonly-tuned ones. Empty
  // string = "not set" = the backend's documented default; placeholder
  // text states that default, it is never written into the input value
  // (a pre-filled mmr would fabricate a margin rate — see the type
  // comment on ScreenerRuleParams.mmr in lib/api/client.ts). Everything
  // the form doesn't expose (strategy, depth_haircut, step_size, …) is
  // preserved from `initial.params` verbatim and merged back in on
  // submit, so editing a rule through this form never silently drops a
  // param someone set through the API directly.
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [slipBps, setSlipBps] = useState(initial?.params?.slip_bps ?? "");
  const [bufferBps, setBufferBps] = useState(initial?.params?.buffer_bps ?? "");
  const [maxHoldH, setMaxHoldH] = useState(
    initial?.params?.max_hold_h ? String(initial.params.max_hold_h) : "",
  );
  const [mmr, setMmr] = useState(initial?.params?.mmr ?? "");

  const submit = () => {
    const params: ScreenerRuleParams = { ...(initial?.params ?? {}) };
    if (slipBps.trim()) params.slip_bps = slipBps.trim();
    else delete params.slip_bps;
    if (bufferBps.trim()) params.buffer_bps = bufferBps.trim();
    else delete params.buffer_bps;
    if (maxHoldH.trim() && Number(maxHoldH) > 0)
      params.max_hold_h = Number(maxHoldH);
    else delete params.max_hold_h;
    if (mmr.trim()) params.mmr = mmr.trim();
    else delete params.mmr;
    // rule_params.go Validate rejects params.strategy=carry|funding_harvest
    // for kind=spread. The form has no control to clear a preserved
    // strategy override, so switching an existing carry/basis rule's kind
    // to spread would otherwise submit an unfixable 400 every time — drop
    // it here rather than leave that dead end.
    if (kind === "spread" && params.strategy !== "cross_venue_spot") {
      delete params.strategy;
    }

    onSave({
      name: name.trim(),
      enabled,
      kind,
      // Absent, not zero, for the threshold the rule's kind doesn't use
      // — a sent 0 would be a real "always fires" threshold to the
      // backend (design §7 notes both fields are kind-dependent; rules.go
      // Validate requires min_spread_bps for kind=spread and
      // min_carry_apr for kind=carry AND kind=basis).
      min_spread_bps:
        kind === "spread" && minSpreadBps.trim()
          ? minSpreadBps.trim()
          : undefined,
      // Percent (form) → fraction (wire) — see the load-side conversion
      // above; ScreenerShared's shiftDecimalPoint keeps this exact
      // (Number()*100/100 reintroduces float noise on a value that
      // round-trips through storage).
      min_carry_apr:
        kind !== "spread" && minCarryApr.trim()
          ? percentToFractionStr(minCarryApr.trim())
          : undefined,
      min_liquidity_quote: minLiquidityQuote.trim() || "0",
      min_lifetime_s: Number(minLifetimeS) || 0,
      buy_venues: buyVenues,
      sell_venues: sellVenues,
      quotes: parseCsv(quotesText),
      bases_allow: parseCsv(basesAllowText),
      bases_deny: parseCsv(basesDenyText),
      cooldown_s: Number(cooldownS) || 0,
      telegram,
      auto_paper: autoPaper,
      paper_size_quote: paperSizeQuote.trim() || "0",
      params: Object.keys(params).length > 0 ? params : undefined,
    });
  };

  return (
    <div className="max-w-2xl space-y-3 rounded border border-[var(--border)] p-3 text-[13px]">
      <div className="grid grid-cols-2 gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-[12px] text-[var(--text-dim)]">Name</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[12px] text-[var(--text-dim)]">Kind</span>
          <select
            value={kind}
            onChange={(e) => setKind(e.target.value as ScreenerRuleKind)}
            className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none"
          >
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </select>
        </label>
        {kind === "spread" ? (
          <label className="flex flex-col gap-1">
            <span className="text-[12px] text-[var(--text-dim)]">
              Min spread (bps)
            </span>
            <input
              value={minSpreadBps}
              onChange={(e) => setMinSpreadBps(e.target.value)}
              inputMode="decimal"
              className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
            />
          </label>
        ) : (
          // kind === "carry" | "basis" — rules.go Validate requires
          // min_carry_apr for both, not just "carry".
          <label className="flex flex-col gap-1">
            <span className="text-[12px] text-[var(--text-dim)]">
              Min carry APR (%)
            </span>
            <input
              value={minCarryApr}
              onChange={(e) => setMinCarryApr(e.target.value)}
              inputMode="decimal"
              className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
            />
          </label>
        )}
        <label className="flex flex-col gap-1">
          <span className="text-[12px] text-[var(--text-dim)]">
            Min liquidity (quote)
          </span>
          <input
            value={minLiquidityQuote}
            onChange={(e) => setMinLiquidityQuote(e.target.value)}
            inputMode="decimal"
            className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[12px] text-[var(--text-dim)]">
            Min lifetime (s)
          </span>
          <input
            value={minLifetimeS}
            onChange={(e) => setMinLifetimeS(e.target.value)}
            inputMode="numeric"
            className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[12px] text-[var(--text-dim)]">
            Cooldown (s)
          </span>
          <input
            value={cooldownS}
            onChange={(e) => setCooldownS(e.target.value)}
            inputMode="numeric"
            className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
          />
        </label>
      </div>

      <div>
        <div className="mb-1 text-[12px] text-[var(--text-dim)]">
          Buy venues
        </div>
        <VenueChips
          selected={buyVenues}
          onToggle={(v) =>
            setBuyVenues((prev) =>
              prev.includes(v) ? prev.filter((x) => x !== v) : [...prev, v],
            )
          }
        />
      </div>
      <div>
        <div className="mb-1 text-[12px] text-[var(--text-dim)]">
          Sell venues
        </div>
        <VenueChips
          selected={sellVenues}
          onToggle={(v) =>
            setSellVenues((prev) =>
              prev.includes(v) ? prev.filter((x) => x !== v) : [...prev, v],
            )
          }
        />
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <label className="flex flex-col gap-1">
          <span className="text-[12px] text-[var(--text-dim)]">
            Quotes (comma-separated)
          </span>
          <input
            value={quotesText}
            onChange={(e) => setQuotesText(e.target.value)}
            className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[12px] text-[var(--text-dim)]">
            Bases allow (comma-separated)
          </span>
          <input
            value={basesAllowText}
            onChange={(e) => setBasesAllowText(e.target.value)}
            className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[12px] text-[var(--text-dim)]">
            Bases deny (comma-separated)
          </span>
          <input
            value={basesDenyText}
            onChange={(e) => setBasesDenyText(e.target.value)}
            className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
          />
        </label>
      </div>

      <label className="flex items-center gap-2">
        <input
          type="checkbox"
          checked={enabled}
          onChange={(e) => setEnabled(e.target.checked)}
        />
        Enabled
      </label>
      <label className="flex items-center gap-2">
        <input
          type="checkbox"
          checked={telegram}
          onChange={(e) => setTelegram(e.target.checked)}
        />
        Push to Telegram
      </label>
      <label className="flex items-center gap-2">
        <input
          type="checkbox"
          checked={autoPaper}
          onChange={(e) => setAutoPaper(e.target.checked)}
        />
        Automatic PAPER execution — live trading is disabled by design
      </label>
      {autoPaper && (
        <label className="flex flex-col gap-1">
          <span className="text-[12px] text-[var(--text-dim)]">
            Paper size (quote)
          </span>
          <input
            value={paperSizeQuote}
            onChange={(e) => setPaperSizeQuote(e.target.value)}
            inputMode="decimal"
            className="w-40 rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
          />
        </label>
      )}

      <div className="border-t border-[var(--border)] pt-3">
        <button
          type="button"
          onClick={() => setShowAdvanced((v) => !v)}
          aria-expanded={showAdvanced}
          className="text-[12px] font-medium text-[var(--accent)] underline"
        >
          {showAdvanced ? "Hide" : "Show"} advanced model inputs
        </button>
        {showAdvanced && (
          <div className="mt-3 grid grid-cols-2 gap-3">
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Slip allowance (bps)
              </span>
              <input
                value={slipBps}
                onChange={(e) => setSlipBps(e.target.value)}
                inputMode="decimal"
                placeholder="default 2"
                className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Buffer (bps)
              </span>
              <input
                value={bufferBps}
                onChange={(e) => setBufferBps(e.target.value)}
                inputMode="decimal"
                placeholder="default 5"
                className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Max hold (hours)
              </span>
              <input
                value={maxHoldH}
                onChange={(e) => setMaxHoldH(e.target.value)}
                inputMode="numeric"
                placeholder="default 720 (30 days)"
                className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Maintenance margin rate (fraction, e.g. 0.005)
              </span>
              <input
                value={mmr}
                onChange={(e) => setMmr(e.target.value)}
                inputMode="decimal"
                placeholder="unknown — carry/basis auto-paper skips entries"
                className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
          </div>
        )}
        <p className="mt-2 text-[11px] text-[var(--text-dim)]">
          Blank = the backend&apos;s documented default (strategy-models.md
          §1–§5); the maintenance margin rate has no default — it is required
          for a carry/basis rule&apos;s automatic PAPER execution to open any
          position (MMR_UNKNOWN otherwise).
        </p>
      </div>

      {error && <p className="text-[var(--critical)]">{error}</p>}
      <div className="flex gap-2">
        <Button onClick={submit} disabled={busy || !name.trim()}>
          {busy ? "Saving…" : "Save rule"}
        </Button>
        <Button onClick={onCancel} danger>
          Cancel
        </Button>
      </div>
    </div>
  );
}

function eventTone(open: boolean): "warn" | "dim" {
  return open ? "warn" : "dim";
}

export default function ScannerAlertsPage() {
  const { state: auth } = useAuth();
  const role = auth.kind === "authenticated" ? auth.me.role : undefined;
  const mayConfig = can(role, "screener:config");

  const [refresh, setRefresh] = useState(0);
  const rules = usePoll(() => api.screener.rules.list(), 10000, [refresh]);
  const [selectedRuleId, setSelectedRuleId] = useState("");
  const events = usePoll(
    () => api.screener.events(selectedRuleId, 100),
    10000,
    [selectedRuleId, refresh],
  );

  const [formState, setFormState] = useState<"new" | ScreenerRule | null>(null);
  const [formBusy, setFormBusy] = useState(false);
  const [formErr, setFormErr] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<ScreenerRule | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const bump = () => setRefresh((n) => n + 1);

  const save = async (input: ScreenerRuleInput) => {
    setFormBusy(true);
    setFormErr("");
    try {
      if (formState && formState !== "new") {
        await api.screener.rules.update(formState.id, input);
        setMsg({ ok: true, text: `Rule "${input.name}" updated.` });
      } else {
        await api.screener.rules.create(input);
        setMsg({ ok: true, text: `Rule "${input.name}" created.` });
      }
      setFormState(null);
      bump();
    } catch (err: unknown) {
      setFormErr(err instanceof ApiError ? err.message : "Save failed.");
    } finally {
      setFormBusy(false);
    }
  };

  const toggleEnabled = async (rule: ScreenerRule) => {
    setMsg(null);
    try {
      const { id, ...rest } = rule;
      await api.screener.rules.update(id, { ...rest, enabled: !rule.enabled });
      bump();
    } catch (err: unknown) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Update failed.",
      });
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    setMsg(null);
    try {
      await api.screener.rules.remove(deleteTarget.id);
      setMsg({ ok: true, text: `Rule "${deleteTarget.name}" deleted.` });
      setDeleteTarget(null);
      bump();
    } catch (err: unknown) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Delete failed.",
      });
      setDeleteTarget(null);
    }
  };

  return (
    <ConsoleShell>
      <PageTitle>Alert Rules</PageTitle>
      {msg && (
        <p
          className={`mb-3 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
        >
          {msg.text}
        </p>
      )}

      <Section title="Rules">
        {mayConfig && formState === null && rules.kind === "ready" && (
          <div className="mb-3">
            <Button onClick={() => setFormState("new")}>New rule…</Button>
          </div>
        )}
        {formState !== null && (
          <div className="mb-4">
            <RuleForm
              initial={formState === "new" ? null : formState}
              busy={formBusy}
              error={formErr}
              onCancel={() => {
                setFormState(null);
                setFormErr("");
              }}
              onSave={save}
            />
          </div>
        )}
        <ScreenerAwait state={rules} what="rules">
          {(list) => (
            <Table
              head={[
                "Name",
                "Kind",
                "Enabled",
                "Threshold",
                "Venues",
                "Cooldown",
                "Telegram",
                "Auto-paper",
                "",
              ]}
              align={[
                "text",
                "text",
                "text",
                "num",
                "text",
                "num",
                "text",
                "text",
                "text",
              ]}
              empty="alert rules"
              label="Alert rules"
              rowKeys={list.map((r) => r.id)}
              rows={list.map((r) => [
                r.name,
                <Badge key="k" tone="dim">
                  {r.kind}
                </Badge>,
                <Badge key="e" tone={r.enabled ? "ok" : "dim"}>
                  {r.enabled ? "enabled" : "disabled"}
                </Badge>,
                r.kind === "spread"
                  ? `${r.min_spread_bps ?? "—"} bps`
                  : // kind === "carry" | "basis" — min_carry_apr is a
                    // FRACTION on the wire; convert for display, never
                    // suffix the raw fraction with "%".
                    `${r.min_carry_apr ? fractionToPercentStr(r.min_carry_apr) : "—"}% APR`,
                `${r.buy_venues.join("/") || "any"} → ${r.sell_venues.join("/") || "any"}`,
                `${r.cooldown_s}s`,
                <Badge key="t" tone={r.telegram ? "ok" : "dim"}>
                  {r.telegram ? "on" : "off"}
                </Badge>,
                <Badge key="a" tone={r.auto_paper ? "warn" : "dim"}>
                  {r.auto_paper ? "on" : "off"}
                </Badge>,
                mayConfig ? (
                  <div key="actions" className="flex flex-wrap gap-1.5">
                    <Button onClick={() => setSelectedRuleId(r.id)}>
                      Events
                    </Button>
                    <Button onClick={() => setFormState(r)}>Edit</Button>
                    <Button onClick={() => toggleEnabled(r)}>
                      {r.enabled ? "Disable" : "Enable"}
                    </Button>
                    <Button onClick={() => setDeleteTarget(r)} danger>
                      Delete
                    </Button>
                  </div>
                ) : (
                  <Button key="ev" onClick={() => setSelectedRuleId(r.id)}>
                    Events
                  </Button>
                ),
              ])}
            />
          )}
        </ScreenerAwait>
      </Section>

      <Section
        title={selectedRuleId ? "Events (selected rule)" : "Events (all rules)"}
      >
        {selectedRuleId && (
          <div className="mb-2">
            <Button onClick={() => setSelectedRuleId("")}>
              Show all rules
            </Button>
          </div>
        )}
        <ScreenerAwait state={events} what="events">
          {(e) => (
            <Table
              head={[
                "Opened",
                "Closed",
                "Lifetime (s)",
                "Pair",
                "Buy → Sell",
                "Peak net bps",
                "Telegram",
                "Paper execution",
              ]}
              align={[
                "text",
                "text",
                "num",
                "text",
                "text",
                "num",
                "text",
                "text",
              ]}
              empty="alert events"
              sticky
              maxHeight={420}
              label="Alert events"
              rowKeys={(e.events ?? []).map(
                (ev) =>
                  `${ev.opened_at}:${ev.base}:${ev.quote}:${ev.buy_venue}:${ev.sell_venue}`,
              )}
              rows={(e.events ?? []).map((ev) => [
                fmtTime(ev.opened_at),
                <Badge key="s" tone={eventTone(!ev.closed_at)}>
                  {ev.closed_at ? fmtTime(ev.closed_at) : "open"}
                </Badge>,
                ev.lifetime_s,
                `${ev.base}/${ev.quote}`,
                `${ev.buy_venue} → ${ev.sell_venue}`,
                signedText(ev.peak_net_bps),
                <Badge key="tg" tone={ev.telegram_sent ? "ok" : "dim"}>
                  {ev.telegram_sent ? "sent" : "not sent"}
                </Badge>,
                ev.paper_execution_id ?? "—",
              ])}
            />
          )}
        </ScreenerAwait>
      </Section>

      {deleteTarget && (
        <ConfirmDialog
          title={`Delete rule "${deleteTarget.name}"?`}
          danger
          confirmLabel="Delete"
          onConfirm={confirmDelete}
          onCancel={() => setDeleteTarget(null)}
          body={
            <p>
              This stops evaluating and alerting on this rule. Past events are
              kept.
            </p>
          }
        />
      )}
    </ConsoleShell>
  );
}
