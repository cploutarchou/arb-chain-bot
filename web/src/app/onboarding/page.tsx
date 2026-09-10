"use client";

// Onboarding wizard (Phase 25, console-v2.md §5): first-run setup for an
// organisation with no screener venues configured and no rules yet —
// pick venues, set simulated paper balances, create a first alert rule.
// Reachable any time from Settings, never a one-shot modal.
//
// Steps 1+2 both mutate the same versioned screener_settings document
// (internal/screener/settings.go) — this wizard fetches it once and
// applies it exactly once, when leaving step 2, rather than twice in a
// row, so there is no stale_version race between the two steps.
//
// Compliance review item 7 (docs/site/copy/onboarding.md
// wizard.rule.body): "There are no default rules and we do not suggest
// assets; every threshold is yours." Step 3 asks only for rule kind and
// numeric thresholds — no pre-filled base/quote asset picks, no starter
// template gallery.

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  api,
  ApiError,
  isStaleVersion,
  type ScreenerRuleKind,
  type ScreenerSettingsDoc,
  type ScreenerSettingsSnapshot,
  type VenueProfile,
} from "@/lib/api/client";
import { useAuth, useEntitlement } from "@/lib/auth";
import { percentToFractionStr } from "@/lib/decimal";
import { ConsoleShell } from "@/components/ConsoleShell";
import { GatedControl } from "@/components/GatedControl";
import {
  Badge,
  Button,
  PageTitle,
  Section,
  StaleVersionNotice,
} from "@/components/ui";

const RULE_KINDS: ScreenerRuleKind[] = ["spread", "carry", "basis"];

type Step = 1 | 2 | 3 | 4;

function StepHeader({ step }: { step: Step }) {
  const labels = ["Venues", "Simulated balances", "First rule", "Done"];
  return (
    <div className="mb-4 flex items-center gap-2 text-[12px] text-[var(--text-dim)]">
      {labels.map((label, i) => {
        const n = (i + 1) as Step;
        return (
          <span
            key={label}
            className={`flex items-center gap-1 ${n === step ? "font-semibold text-[var(--text)]" : ""}`}
          >
            {i > 0 && <span className="text-[var(--border-strong)]">→</span>}
            {n}. {label}
          </span>
        );
      })}
    </div>
  );
}

export default function OnboardingPage() {
  const { state: auth } = useAuth();
  const screenerMax = useEntitlement("venues.screener_max");
  const screenerFixed = useEntitlement("venues.screener_fixed");
  const ruleKindsAllowed = useEntitlement("rules.kinds");
  const packageCode =
    auth.kind === "authenticated"
      ? auth.me.entitlements.package_code
      : undefined;

  const [step, setStep] = useState<Step>(1);
  const [loadErr, setLoadErr] = useState("");
  const [capabilities, setCapabilities] = useState<VenueProfile[] | null>(null);
  const [snapshot, setSnapshot] = useState<ScreenerSettingsSnapshot | null>(
    null,
  );

  const [selectedVenues, setSelectedVenues] = useState<string[]>([]);
  const [assets, setAssets] = useState<string[]>(["USDT"]);
  const [newAsset, setNewAsset] = useState("");
  const [balances, setBalances] = useState<
    Record<string, Record<string, string>>
  >({});

  const [applyBusy, setApplyBusy] = useState(false);
  const [applyErr, setApplyErr] = useState("");
  const [staleCurrent, setStaleCurrent] = useState<number | null>(null);
  const [fieldTiming, setFieldTiming] = useState<Record<string, string>>({});

  const [ruleKind, setRuleKind] = useState<ScreenerRuleKind>("spread");
  const [threshold, setThreshold] = useState("");
  const [cooldownS, setCooldownS] = useState("300");
  const [ruleBusy, setRuleBusy] = useState(false);
  const [ruleErr, setRuleErr] = useState("");
  const [ruleSavedName, setRuleSavedName] = useState<string | null>(null);
  const [ruleSkipped, setRuleSkipped] = useState(false);

  useEffect(() => {
    let cancelled = false;
    Promise.all([api.platform.capabilities(), api.screener.settings.current()])
      .then(([caps, snap]) => {
        if (cancelled) return;
        setCapabilities(caps.venues);
        setSnapshot(snap);
        const preselected = Object.entries(snap.settings.venues)
          .filter(([, v]) => v.enabled)
          .map(([id]) => id);
        setSelectedVenues(preselected);
        // Seed from whatever is already saved (this wizard is reachable
        // any time from Settings, not just on a genuinely empty first
        // run — console-v2.md §5) so re-running it never discards an
        // existing balance the operator didn't touch this time.
        const existingBalances = snap.settings.paper.balances ?? {};
        setBalances(existingBalances);
        const existingAssets = Array.from(
          new Set(
            Object.values(existingBalances).flatMap((byAsset) =>
              Object.keys(byAsset),
            ),
          ),
        );
        if (existingAssets.length > 0) setAssets(existingAssets);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setLoadErr(
          e instanceof ApiError
            ? e.message
            : "Screener backend not available in this build.",
        );
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const fixedVenues =
    screenerFixed && screenerFixed.length > 0 ? screenerFixed : null;
  const maxVenues = fixedVenues ? fixedVenues.length : screenerMax;

  const toggleVenue = (id: string) => {
    if (fixedVenues) return;
    setSelectedVenues((prev) => {
      if (prev.includes(id)) return prev.filter((v) => v !== id);
      if (
        maxVenues !== undefined &&
        maxVenues !== -1 &&
        prev.length >= maxVenues
      ) {
        return prev;
      }
      return [...prev, id];
    });
  };

  const effectiveSelectedVenues = fixedVenues ?? selectedVenues;

  const setBalance = (venue: string, asset: string, value: string) => {
    setBalances((prev) => ({
      ...prev,
      [venue]: { ...(prev[venue] ?? {}), [asset]: value },
    }));
  };

  const addAsset = () => {
    const a = newAsset.trim().toUpperCase();
    if (!a || assets.includes(a)) return;
    setAssets((prev) => [...prev, a]);
    setNewAsset("");
  };

  const applySettingsAndAdvance = async () => {
    if (!snapshot) return;
    setApplyBusy(true);
    setApplyErr("");
    setStaleCurrent(null);
    const venues: ScreenerSettingsDoc["venues"] = {
      ...snapshot.settings.venues,
    };
    for (const id of Object.keys(venues)) {
      const current = venues[id];
      if (!current) continue;
      venues[id] = {
        ...current,
        enabled: effectiveSelectedVenues.includes(id),
      };
    }
    // Drop cleared-but-not-deleted entries (a blank input, not "0") —
    // an empty string is not a parseable decimal and must never reach
    // the backend as one.
    const cleanedBalances: Record<string, Record<string, string>> = {};
    for (const [venue, byAsset] of Object.entries(balances)) {
      const kept = Object.fromEntries(
        Object.entries(byAsset).filter(([, v]) => v.trim() !== ""),
      );
      if (Object.keys(kept).length > 0) cleanedBalances[venue] = kept;
    }
    const doc: ScreenerSettingsDoc = {
      poll_interval_s: snapshot.settings.poll_interval_s,
      min_liquidity_quote: snapshot.settings.min_liquidity_quote,
      venues,
      paper: { balances: cleanedBalances },
    };
    try {
      const res = await api.screener.settings.apply(doc, snapshot.version);
      setSnapshot(res);
      setFieldTiming(res.field_timing ?? {});
      setStep(3);
    } catch (e: unknown) {
      if (isStaleVersion(e)) {
        const data = (e as ApiError).data as {
          current_version?: number;
        } | null;
        setStaleCurrent(data?.current_version ?? null);
      } else {
        setApplyErr(
          e instanceof ApiError ? e.message : "Could not save settings.",
        );
      }
    } finally {
      setApplyBusy(false);
    }
  };

  const reloadSettings = async () => {
    try {
      const snap = await api.screener.settings.current();
      setSnapshot(snap);
      setStaleCurrent(null);
      setApplyErr("");
    } catch (e: unknown) {
      setApplyErr(e instanceof ApiError ? e.message : "Reload failed.");
    }
  };

  const allowedRuleKinds = ruleKindsAllowed
    ? RULE_KINDS.filter((k) => ruleKindsAllowed.includes(k))
    : RULE_KINDS;

  const saveRule = async () => {
    // "no default rules and we do not suggest assets; every threshold is
    // yours" (onboarding.md wizard.rule.body) — an unset threshold must
    // never silently become an always-fires 0, so the Save button stays
    // disabled until one is typed (see the JSX below); this is the
    // belt-and-braces check for the same rule.
    if (!threshold.trim()) {
      setRuleErr("Enter a threshold before saving.");
      return;
    }
    setRuleBusy(true);
    setRuleErr("");
    try {
      const name = `First rule (${ruleKind})`;
      await api.screener.rules.create({
        name,
        enabled: true,
        kind: ruleKind,
        min_spread_bps: ruleKind === "spread" ? threshold.trim() : undefined,
        // Wire contract is a fraction (e.g. 0.10 = 10%); the field below
        // is labelled "%" for the operator. percentToFractionStr shifts
        // the decimal point on the digits themselves — never
        // `Number(x) / 100`, which persists float noise (a typed "1.1"
        // becoming "0.011000000000000001") on a threshold that gates
        // automatic paper execution.
        min_carry_apr:
          ruleKind !== "spread" && threshold.trim()
            ? percentToFractionStr(threshold.trim())
            : undefined,
        min_liquidity_quote: "0",
        min_lifetime_s: 0,
        buy_venues: effectiveSelectedVenues,
        sell_venues: effectiveSelectedVenues,
        // Compliance review #7 — no default asset picks: empty means no
        // base/quote restriction, never a pre-filled pair.
        quotes: [],
        bases_allow: [],
        bases_deny: [],
        cooldown_s: Number(cooldownS) || 0,
        telegram: false,
        auto_paper: false,
        paper_size_quote: "0",
      });
      setRuleSavedName(name);
      setStep(4);
    } catch (e: unknown) {
      setRuleErr(
        e instanceof ApiError ? e.message : "Could not save the rule.",
      );
    } finally {
      setRuleBusy(false);
    }
  };

  const skipRule = () => {
    setRuleSkipped(true);
    setStep(4);
  };

  if (loadErr) {
    return (
      <ConsoleShell active="Onboarding">
        <PageTitle>Set up your organisation</PageTitle>
        <p className="text-[13px] text-[var(--text-dim)]">{loadErr}</p>
      </ConsoleShell>
    );
  }

  return (
    <ConsoleShell active="Onboarding">
      <PageTitle>Set up your organisation</PageTitle>
      <p className="mb-4 max-w-2xl text-[13px] text-[var(--text-dim)]">
        Three short steps. You can change everything later from Settings. Public
        market data only — the screener never uses your exchange API keys, and
        this console never asks for one.
      </p>
      <StepHeader step={step} />

      {step === 1 && (
        <Section title="Step 1 — Pick venues">
          {!capabilities ? (
            <p className="text-[13px] text-[var(--text-dim)]">
              Loading venues…
            </p>
          ) : (
            <>
              {fixedVenues && (
                <p className="mb-2 text-[12px] text-[var(--text-dim)]">
                  Your package uses {fixedVenues.length} fixed venue
                  {fixedVenues.length === 1 ? "" : "s"}. Upgrade to choose your
                  own.
                </p>
              )}
              <div className="grid max-w-2xl grid-cols-1 gap-2 sm:grid-cols-2">
                {capabilities.map((v) => {
                  const selected = effectiveSelectedVenues.includes(v.id);
                  const disabledByFixed = fixedVenues !== null;
                  const atMax =
                    !selected &&
                    !disabledByFixed &&
                    maxVenues !== undefined &&
                    maxVenues !== -1 &&
                    selectedVenues.length >= maxVenues;
                  if (atMax) {
                    return (
                      <GatedControl
                        key={v.id}
                        as="chip"
                        state="package"
                        reason=""
                        upgradeHref="/billing"
                        packageName="a higher package"
                      >
                        {v.name}
                      </GatedControl>
                    );
                  }
                  return (
                    <label
                      key={v.id}
                      className={`flex items-center gap-2 rounded border px-2 py-1.5 text-[13px] ${
                        v.available
                          ? "border-[var(--border-strong)] cursor-pointer"
                          : "border-[var(--border)] cursor-not-allowed opacity-60"
                      }`}
                    >
                      <input
                        type="checkbox"
                        checked={selected}
                        disabled={!v.available || disabledByFixed}
                        onChange={() => toggleVenue(v.id)}
                      />
                      <span className="flex-1">{v.name}</span>
                      {v.available ? (
                        <Badge tone="ok">available</Badge>
                      ) : (
                        <Badge tone="dim">{v.reason ?? "unavailable"}</Badge>
                      )}
                    </label>
                  );
                })}
              </div>
              <Button
                onClick={() => setStep(2)}
                disabled={effectiveSelectedVenues.length === 0}
              >
                Next: paper balances
              </Button>
            </>
          )}
        </Section>
      )}

      {step === 2 && (
        <Section title="Step 2 — Set paper balances">
          <p className="mb-2 max-w-2xl text-[12px] text-[var(--text-dim)]">
            These are simulated balances for automatic paper execution — no real
            funds are held or moved. You can change them later from Settings →
            Venues &amp; fees.
          </p>
          {staleCurrent !== null && (
            <StaleVersionNotice
              currentVersion={staleCurrent}
              onReload={reloadSettings}
            />
          )}
          <div className="max-w-2xl overflow-x-auto">
            <table className="w-full border-collapse text-[13px]">
              <thead>
                <tr className="border-b border-[var(--border)] text-left text-[12px] uppercase tracking-wider text-[var(--text-dim)]">
                  <th className="py-1.5 pr-3">Venue</th>
                  {assets.map((a) => (
                    <th key={a} className="py-1.5 pr-3">
                      Simulated balance ({a})
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {effectiveSelectedVenues.map((venue) => (
                  <tr key={venue} className="border-b border-[var(--border)]">
                    <td className="py-1.5 pr-3 text-[var(--text)]">{venue}</td>
                    {assets.map((a) => (
                      <td key={a} className="py-1.5 pr-3">
                        <input
                          value={balances[venue]?.[a] ?? ""}
                          onChange={(e) => setBalance(venue, a, e.target.value)}
                          inputMode="decimal"
                          placeholder="0"
                          aria-label={`Simulated balance for ${venue} ${a}`}
                          className="w-28 rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 text-right outline-none focus:border-[var(--accent)]"
                        />
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="mt-2 flex items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Add asset
              </span>
              <input
                value={newAsset}
                onChange={(e) => setNewAsset(e.target.value)}
                placeholder="USDC"
                className="w-28 rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 text-[13px] outline-none focus:border-[var(--accent)]"
              />
            </label>
            <Button onClick={addAsset} disabled={!newAsset.trim()}>
              Add
            </Button>
          </div>
          {applyErr && (
            <p className="mt-2 text-[13px] text-[var(--critical)]">
              {applyErr}
            </p>
          )}
          <div className="mt-3 flex gap-2">
            <Button onClick={() => setStep(1)}>Back</Button>
            <Button
              onClick={() => void applySettingsAndAdvance()}
              disabled={applyBusy}
            >
              {applyBusy ? "Saving…" : "Next: create your first rule"}
            </Button>
          </div>
        </Section>
      )}

      {step === 3 && (
        <Section title="Step 3 — Create your first rule">
          <p className="mb-2 max-w-2xl text-[12px] text-[var(--text-dim)]">
            A rule is a measurement you want to be told about. There are no
            default rules and we do not suggest assets; every threshold is
            yours.
          </p>
          <div className="grid max-w-lg grid-cols-2 gap-3 text-[13px]">
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Rule type
              </span>
              <select
                value={ruleKind}
                onChange={(e) =>
                  setRuleKind(e.target.value as ScreenerRuleKind)
                }
                className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none"
              >
                {allowedRuleKinds.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                {ruleKind === "spread"
                  ? "Net threshold (bps, after fees)"
                  : "Min carry APR (%)"}
              </span>
              <input
                value={threshold}
                onChange={(e) => setThreshold(e.target.value)}
                inputMode="decimal"
                className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[12px] text-[var(--text-dim)]">
                Cool-down between alerts (seconds)
              </span>
              <input
                value={cooldownS}
                onChange={(e) => setCooldownS(e.target.value)}
                inputMode="numeric"
                className="rounded border border-[var(--border-strong)] bg-[var(--bg)] px-2 py-1 outline-none focus:border-[var(--accent)]"
              />
            </label>
          </div>
          {ruleErr && (
            <p className="mt-2 text-[13px] text-[var(--critical)]">{ruleErr}</p>
          )}
          <div className="mt-3 flex gap-2">
            <Button onClick={skipRule}>Skip for now</Button>
            <Button
              onClick={() => void saveRule()}
              disabled={ruleBusy || !threshold.trim()}
            >
              {ruleBusy ? "Saving…" : "Save rule"}
            </Button>
          </div>
        </Section>
      )}

      {step === 4 && (
        <Section title="Setup saved">
          <p className="mb-2 max-w-2xl text-[13px] text-[var(--text)]">
            Setup saved as screener configuration
            {snapshot ? ` v${snapshot.version}` : ""}. Venues:{" "}
            {effectiveSelectedVenues.join(", ") || "none"}.{" "}
            {ruleSavedName
              ? `Rule "${ruleSavedName}" created.`
              : ruleSkipped
                ? "No rule created yet."
                : ""}
          </p>
          {Object.keys(fieldTiming).length > 0 && (
            <ul className="mb-3 list-inside list-disc text-[12px] text-[var(--text-dim)]">
              {Object.entries(fieldTiming).map(([field, timing]) => (
                <li key={field}>
                  {field}: {timing}
                </li>
              ))}
            </ul>
          )}
          <div className="flex gap-3 text-[13px]">
            <Link href="/screener" className="text-[var(--accent)] underline">
              Go to Screener
            </Link>
            <Link
              href="/scanner-alerts"
              className="text-[var(--accent)] underline"
            >
              Go to Alert Rules
            </Link>
          </div>
          {packageCode && (
            <p className="mt-3 text-[12px] text-[var(--text-dim)]">
              Package: {packageCode}
            </p>
          )}
        </Section>
      )}
    </ConsoleShell>
  );
}
