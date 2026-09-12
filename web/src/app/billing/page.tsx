"use client";

// Billing (T-083, docs/design/billing.md §1.5): current subscription,
// package cards sourced only from GET /billing/prices (price_id,
// package_code, billing_interval — no amount; Paddle previews the price,
// packages.md §4: "never hard-coded copies of the table above"), Paddle
// Checkout overlay, the customer portal, and cancellation. Checkout/
// portal/cancel need OWNER or ADMIN (internal/api/billingapi.go
// requireOrgManager); everyone else sees the subscription read-only.
//
// Paddle.js loads on this page only (next/script, page-scoped) and only
// once a client_token is present; without one billing shows "Billing not
// configured" rather than a broken overlay.

import { useEffect, useRef, useState } from "react";
import Script from "next/script";
import {
  api,
  ApiError,
  type AffiliatePayoutLine,
  type BillingPrice,
  type BillingSubscriptionResponse,
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
  fmtTime,
} from "@/components/ui";

// Minimal Paddle.js v2 surface this page uses — no full SDK typing here,
// the console never depends on more of Paddle.js than Initialize +
// Checkout.open (paddle:checkout-web).
interface PaddleGlobal {
  Environment?: { set: (env: string) => void };
  Initialize: (opts: { token: string }) => void;
  Checkout: { open: (opts: { transactionId: string }) => void };
}
declare global {
  interface Window {
    Paddle?: PaddleGlobal;
  }
}

const PACKAGE_NAME: Record<string, string> = {
  watch: "Watch",
  signal: "Signal",
  operator: "Operator",
  desk: "Desk",
  institution: "Institution",
};

function packageLabel(code: string): string {
  return PACKAGE_NAME[code] ?? code;
}

function statusTone(status: string): "ok" | "warn" | "bad" | "dim" {
  if (status === "active" || status === "trialing") return "ok";
  if (status === "past_due") return "warn";
  if (status === "canceled" || status === "paused") return "dim";
  return "dim";
}

// Affiliate payouts (T-084, packages.md §5): the operator's monthly
// report — who is payable at what matured balance, the fraud-rule-3
// refund rate over 90 days, and the clawback exposure — plus the
// audited recording of a payout as a ledger row. Money moves outside
// the platform (bank transfer / PayPal); this records what left.
function AffiliatePayouts() {
  const [refresh, setRefresh] = useState(0);
  const report = usePoll(() => api.billing.affiliatePayouts(), 60000, [refresh]);
  const [target, setTarget] = useState<AffiliatePayoutLine | null>(null);
  const [amount, setAmount] = useState("");
  const [reference, setReference] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const openDialog = (line: AffiliatePayoutLine) => {
    setTarget(line);
    setAmount(line.payout.balance.matured);
    setReference("");
    setMsg(null);
  };

  const confirm = async () => {
    if (!target) return;
    setBusy(true);
    try {
      await api.billing.recordAffiliatePayout(target.id, amount, reference);
      setMsg({ ok: true, text: `Payout of $${amount} recorded for ${target.code}.` });
      setTarget(null);
      setRefresh((n) => n + 1);
    } catch (err: unknown) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Recording the payout failed.",
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section title="Affiliate payouts">
      {msg && (
        <p
          className={`mb-3 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
        >
          {msg.text}
        </p>
      )}
      <Await state={report} what="affiliate payouts">
        {(data) =>
          data.accounts.length === 0 ? (
            <p className="text-[13px] text-[var(--text-dim)]">
              No affiliate accounts exist yet.
            </p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full max-w-4xl border-collapse text-[13px]">
                <thead>
                  <tr className="border-b border-[var(--border)] text-left text-[12px] uppercase tracking-wider text-[var(--text-dim)]">
                    <th className="py-2 pr-3">Account</th>
                    <th className="py-2 pr-3">Status</th>
                    <th className="py-2 pr-3">Accrued</th>
                    <th className="py-2 pr-3">Matured (unpaid)</th>
                    <th className="py-2 pr-3">Paid</th>
                    <th className="py-2 pr-3">Refunds 90d</th>
                    <th className="py-2 pr-3"></th>
                  </tr>
                </thead>
                <tbody>
                  {data.accounts.map((line) => (
                    <tr key={line.id} className="border-b border-[var(--border)]">
                      <td className="py-2 pr-3 font-medium text-[var(--text)]">
                        {line.code}
                      </td>
                      <td className="py-2 pr-3">
                        <Badge
                          tone={
                            line.status === "active"
                              ? "ok"
                              : line.status === "review"
                                ? "warn"
                                : "dim"
                          }
                        >
                          {line.status}
                        </Badge>
                        {line.payout.manual_review && (
                          <span className="ml-2 text-[12px] text-[var(--warn)]">
                            manual review
                          </span>
                        )}
                      </td>
                      <td className="py-2 pr-3 text-[var(--text-dim)]">
                        ${line.payout.balance.accrued}
                      </td>
                      <td className="py-2 pr-3 font-medium text-[var(--text)]">
                        ${line.payout.balance.matured}
                        {line.payout.balance.payable && (
                          <span className="ml-2 text-[12px] text-[var(--ok)]">
                            payable
                          </span>
                        )}
                      </td>
                      <td className="py-2 pr-3 text-[var(--text-dim)]">
                        ${line.payout.balance.paid}
                        {line.payout.paid_last_180d !== "0" && (
                          <span className="ml-1 text-[12px] text-[var(--text-dim)]">
                            (${line.payout.paid_last_180d} in clawback window)
                          </span>
                        )}
                      </td>
                      <td className="py-2 pr-3 text-[var(--text-dim)]">
                        {line.payout.refund_rate_90d === undefined
                          ? "—"
                          : `${(Number(line.payout.refund_rate_90d) * 100).toFixed(1)}%`}
                      </td>
                      <td className="py-2 pr-3">
                        {line.status !== "closed" &&
                          line.payout.balance.matured !== "0" && (
                            <Button onClick={() => openDialog(line)}>
                              Record payout
                            </Button>
                          )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="mt-2 text-[12px] text-[var(--text-dim)]">
                Paid monthly on the 15th for the previous calendar month at the
                $100 matured threshold. Money moves via Paddle-supported
                transfer; recording here writes the ledger row.
              </p>
            </div>
          )
        }
      </Await>
      {target && (
        <ConfirmDialog
          title={`Record payout for ${target.code}?`}
          confirmLabel={busy ? "Recording…" : "Record payout"}
          confirmDisabled={busy || !amount || !reference}
          onConfirm={() => void confirm()}
          onCancel={() => setTarget(null)}
          body={
            <div className="space-y-3">
              <p>
                Matured unpaid balance: ${target.payout.balance.matured}. The
                amount must not exceed it; the ledger row cites the reference.
              </p>
              <label className="block text-[13px]">
                <span className="mb-1 block text-[12px] text-[var(--text-dim)]">
                  Amount (USD)
                </span>
                <input
                  className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] text-[var(--text)]"
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                  inputMode="decimal"
                />
              </label>
              <label className="block text-[13px]">
                <span className="mb-1 block text-[12px] text-[var(--text-dim)]">
                  Payout reference (e.g. 2026-09)
                </span>
                <input
                  className="w-full rounded border border-[var(--border)] bg-[var(--bg)] px-2 py-1 text-[13px] text-[var(--text)]"
                  value={reference}
                  onChange={(e) => setReference(e.target.value)}
                />
              </label>
            </div>
          }
        />
      )}
    </Section>
  );
}

function SubscriptionSummary({ data }: { data: BillingSubscriptionResponse }) {
  const sub = data.subscription;
  return (
    <Section title="Current subscription">
      <div className="max-w-2xl space-y-2 text-[13px]">
        <div className="flex items-center gap-2">
          <span className="text-[12px] text-[var(--text-dim)]">Package</span>
          <span className="font-medium text-[var(--text)]">
            {packageLabel(data.package_code)}
          </span>
          {sub && <Badge tone={statusTone(sub.status)}>{sub.status}</Badge>}
        </div>
        {data.trial_ends_at && (
          <p className="text-[12px] text-[var(--text-dim)]">
            Trial ends {fmtTime(data.trial_ends_at)}. No card is on file.
          </p>
        )}
        {sub?.current_period_end && (
          <p className="text-[12px] text-[var(--text-dim)]">
            Current period ends {fmtTime(sub.current_period_end)}
            {sub.cancel_at_period_end ? " — cancellation scheduled" : ""}
            {sub.scheduled_price_id
              ? " — a package change is scheduled for renewal"
              : ""}
          </p>
        )}
        {sub?.past_due_since && (
          <p className="text-[12px] text-[var(--warn)]">
            Payment failed. Full access continues for 7 days, then alerts and
            simulated execution pause until payment succeeds.
          </p>
        )}
        {data.status?.read_only && (
          <p className="text-[12px] text-[var(--critical)]">
            This organisation is currently read-only (alerts and simulated
            execution paused) — resolve the payment issue to restore access.
          </p>
        )}
      </div>
    </Section>
  );
}

export default function BillingPage() {
  const { state: auth } = useAuth();
  const [refresh, setRefresh] = useState(0);
  const bump = () => setRefresh((n) => n + 1);
  const sub = usePoll(() => api.billing.subscription(), 20000, [refresh]);
  const prices = usePoll(() => api.billing.prices(), 60000, [refresh]);

  const orgRole = auth.kind === "authenticated" ? auth.me.org_role : undefined;
  const platformAdmin = auth.kind === "authenticated" && auth.me.platform_admin;
  const canManage = platformAdmin || orgRole === "OWNER" || orgRole === "ADMIN";

  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busyPriceId, setBusyPriceId] = useState<string | null>(null);
  const [cancelDialog, setCancelDialog] = useState(false);
  const [cancelBusy, setCancelBusy] = useState(false);
  const paddleReady = useRef(false);

  const clientToken = prices.kind === "ready" ? prices.data.client_token : "";
  const billingUnavailable =
    (sub.kind === "error" &&
      (sub.code === "billing_unavailable" ||
        sub.code === "billing_unconfigured")) ||
    (prices.kind === "ready" && !prices.data.client_token);

  useEffect(() => {
    if (!clientToken || paddleReady.current || typeof window === "undefined")
      return;
    if (window.Paddle) {
      if (prices.kind === "ready" && prices.data.environment === "sandbox") {
        window.Paddle.Environment?.set("sandbox");
      }
      window.Paddle.Initialize({ token: clientToken });
      paddleReady.current = true;
    }
  }, [clientToken, prices]);

  const onPaddleLoad = () => {
    if (!clientToken || typeof window === "undefined" || !window.Paddle) return;
    if (prices.kind === "ready" && prices.data.environment === "sandbox") {
      window.Paddle.Environment?.set("sandbox");
    }
    window.Paddle.Initialize({ token: clientToken });
    paddleReady.current = true;
  };

  const choose = async (price: BillingPrice) => {
    setMsg(null);
    setBusyPriceId(price.price_id);
    try {
      const res = await api.billing.checkout(price.price_id);
      if (res.pending) {
        // packages.md §4: entitlements change only on a verified webhook,
        // never on this response — show "pending", never flip the
        // displayed package optimistically.
        setMsg({
          ok: true,
          text: "Package change pending confirmation from the payment processor.",
        });
      } else if (res.transaction_id && window.Paddle) {
        window.Paddle.Checkout.open({ transactionId: res.transaction_id });
      } else {
        setMsg({ ok: false, text: "Billing not configured." });
      }
      bump();
    } catch (err: unknown) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Checkout failed.",
      });
    } finally {
      setBusyPriceId(null);
    }
  };

  const openPortal = async () => {
    setMsg(null);
    try {
      const { url } = await api.billing.portal();
      window.open(url, "_blank", "noopener,noreferrer");
    } catch (err: unknown) {
      setMsg({
        ok: false,
        text:
          err instanceof ApiError
            ? err.message
            : "Could not open the billing portal.",
      });
    }
  };

  const confirmCancel = async () => {
    setCancelBusy(true);
    try {
      await api.billing.cancel();
      setMsg({
        ok: true,
        text: "Cancellation scheduled for the end of the current billing period.",
      });
      setCancelDialog(false);
      bump();
    } catch (err: unknown) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Cancel failed.",
      });
    } finally {
      setCancelBusy(false);
    }
  };

  const pricesByPackage = new Map<string, BillingPrice[]>();
  if (prices.kind === "ready") {
    for (const p of prices.data.prices ?? []) {
      const list = pricesByPackage.get(p.package_code) ?? [];
      list.push(p);
      pricesByPackage.set(p.package_code, list);
    }
  }
  const packageOrder = ["watch", "signal", "operator", "desk", "institution"];
  const hasActiveSubscription =
    sub.kind === "ready" && sub.data.status?.subscription === "active";

  return (
    <ConsoleShell active="Billing">
      <PageTitle>Billing</PageTitle>

      {clientToken && (
        <Script
          id="paddle-js"
          src="https://cdn.paddle.com/paddle/v2/paddle.js"
          strategy="afterInteractive"
          onLoad={onPaddleLoad}
        />
      )}

      {msg && (
        <p
          className={`mb-3 text-[13px] ${msg.ok ? "text-[var(--ok)]" : "text-[var(--critical)]"}`}
        >
          {msg.text}
        </p>
      )}

      {sub.kind === "loading" && (
        <p className="text-[13px] text-[var(--text-dim)]">Loading…</p>
      )}
      {sub.kind === "ready" && <SubscriptionSummary data={sub.data} />}
      {billingUnavailable && (
        <Section title="Current subscription">
          <p className="text-[13px] text-[var(--text-dim)]">
            Billing not configured.
          </p>
        </Section>
      )}
      {sub.kind === "error" && !billingUnavailable && (
        <p className="text-[13px] text-[var(--critical)]">{sub.message}</p>
      )}

      {canManage && (
        <Section title="Manage">
          <div className="flex flex-wrap gap-2">
            <Button
              onClick={() => void openPortal()}
              disabled={billingUnavailable}
            >
              Manage billing
            </Button>
            {hasActiveSubscription && (
              <Button onClick={() => setCancelDialog(true)} danger>
                Cancel subscription
              </Button>
            )}
          </div>
        </Section>
      )}

      <Section title="Packages">
        {billingUnavailable ? (
          <p className="text-[13px] text-[var(--text-dim)]">
            Billing not configured.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full max-w-4xl border-collapse text-[13px]">
              <thead>
                <tr className="border-b border-[var(--border)] text-left text-[12px] uppercase tracking-wider text-[var(--text-dim)]">
                  <th className="py-2 pr-3">Package</th>
                  <th className="py-2 pr-3">Billing</th>
                  <th className="py-2 pr-3"></th>
                </tr>
              </thead>
              <tbody>
                {packageOrder
                  .filter((code) => pricesByPackage.has(code))
                  .map((code) =>
                    (pricesByPackage.get(code) ?? []).map((price, i) => (
                      <tr
                        key={price.price_id}
                        className="border-b border-[var(--border)]"
                      >
                        {i === 0 && (
                          <td
                            className="py-2 pr-3 font-medium text-[var(--text)]"
                            rowSpan={pricesByPackage.get(code)?.length}
                          >
                            {packageLabel(code)}
                          </td>
                        )}
                        <td className="py-2 pr-3 text-[var(--text-dim)]">
                          {price.billing_interval === "year"
                            ? "Annual"
                            : "Monthly"}
                        </td>
                        <td className="py-2 pr-3">
                          {canManage ? (
                            <Button
                              onClick={() => void choose(price)}
                              disabled={busyPriceId === price.price_id}
                            >
                              {busyPriceId === price.price_id
                                ? "Working…"
                                : hasActiveSubscription
                                  ? "Upgrade"
                                  : "Choose"}
                            </Button>
                          ) : (
                            <span className="text-[12px] text-[var(--text-dim)]">
                              Requires OWNER or ADMIN
                            </span>
                          )}
                        </td>
                      </tr>
                    )),
                  )}
                <tr>
                  <td className="py-2 pr-3 font-medium text-[var(--text-dim)]">
                    Live execution
                  </td>
                  <td className="py-2 pr-3 text-[var(--text-dim)]">—</td>
                  <td className="py-2 pr-3 text-[12px] text-[var(--text-dim)]">
                    Not offered
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        )}
      </Section>

      {platformAdmin && <AffiliatePayouts />}

      {cancelDialog && (
        <ConfirmDialog
          title="Cancel subscription?"
          danger
          confirmLabel={cancelBusy ? "Cancelling…" : "Cancel subscription"}
          confirmDisabled={cancelBusy}
          onConfirm={confirmCancel}
          onCancel={() => setCancelDialog(false)}
          body={
            <p>
              Access continues until the end of the current billing period, then
              the organisation moves to Watch. No refund is issued except the
              14-day money-back window on a new subscription.
            </p>
          }
        />
      )}
    </ConsoleShell>
  );
}
