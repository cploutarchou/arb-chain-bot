import Link from "next/link";
import { pageMeta } from "@/lib/meta";
import { brand } from "@/lib/tokens";
import { Container } from "@/components/Page";
import { Icon } from "@/components/Icons";

// The affiliate programme is not open (packages.md §7: it opens only after
// compliance sign-off of the terms). This page states what the programme
// will be, links the draft terms, and makes no rate claim: rates are
// {{aff.*}} tokens in the terms, resolved at publication.

export const metadata = pageMeta(
  "Affiliates",
  "The affiliate programme: eligibility, attribution, what you may and may not say. Not yet open.",
  "/affiliates",
);

export default function Affiliates() {
  return (
    <>
      <section className="band border-b border-[var(--border)]">
        <Container className="py-16 md:py-24">
          <p className="t-caption uppercase tracking-wide">Affiliates · programme not yet open</p>
          <h1 className="t-display mt-3 max-w-[68ch]">Refer people to a tool that publishes its misses.</h1>
          <p className="t-lead mt-5 max-w-[68ch] text-[var(--text-dim)]">
            The {brand} affiliate programme pays commission on referred paid
            subscriptions, with attribution by link or code, a maturation
            period that covers the money-back window, and a short list of
            things you may not say. It opens after the terms have passed
            legal review.
          </p>
        </Container>
      </section>

      <section className="py-12 md:py-16">
        <Container>
          <h2 className="t-h2">How it works</h2>
          <div className="mt-8 grid gap-6 md:grid-cols-3">
            {[
              ["Attribution", "Referral by link or code. Last click wins. Attribution storage lasts forty-five days from the click; an organisation is bound to its referrer when it is created."],
              ["Maturation and payout", "Commission on a payment becomes payable forty-five days after it cleared. Payouts are monthly once your matured balance reaches the threshold stated in the terms. No crypto payouts."],
              ["Your dashboard", "Clicks, sign-ups, trials, conversions, and accrued, matured and paid amounts, every figure read from the ledger. Nothing is estimated."],
            ].map(([t, b]) => (
              <div key={t} className="rounded border border-[var(--border)] bg-[var(--bg-panel)] p-5">
                <h3 className="t-h3">{t}</h3>
                <p className="t-small mt-2 text-[var(--text-dim)]">{b}</p>
              </div>
            ))}
          </div>
        </Container>
      </section>

      <section className="py-12 md:py-16">
        <Container>
          <h2 className="t-h2">What you may say</h2>
          <div className="prose mt-6">
            <p>
              You may describe what the product measures and how. You may
              quote a published report from the campaign archive verbatim,
              with its fees and its verdict. You may say nothing about
              performance. You must disclose the affiliate relationship
              wherever you place a link, and include this sentence wherever
              you describe the product:
            </p>
            <blockquote>
              {brand} is a market-data and simulation tool. It does not
              trade, hold funds or give advice.
            </blockquote>
            <p>
              Material that describes results or the risk of trading must be
              submitted to us before publication. Any stated or implied
              return, hit rate, spread size or income, any invented figure,
              brand bidding, coupon traffic, incentivised sign-ups and
              self-referral end participation. The full list is in §5 of the{" "}
              <Link href="/legal/affiliate-terms">Affiliate Programme Terms</Link>.
            </p>
          </div>
        </Container>
      </section>

      <section className="py-12">
        <Container>
          <div className="flex items-start gap-3 rounded border border-[var(--border-strong)] bg-[var(--bg-panel)] p-5">
            <Icon name="shield" size={24} className="shrink-0 text-[var(--accent)]" />
            <div className="t-small">
              <strong>Commission rates and the payout threshold</strong> are
              set in the terms at publication and are not stated here. Rates
              are proposals until the programme opens; sanctions screening
              applies at enrolment and before each payout.
            </div>
          </div>
        </Container>
      </section>
    </>
  );
}
