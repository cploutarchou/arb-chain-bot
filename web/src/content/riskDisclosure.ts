// Risk disclosure copy shown by the blocking acknowledgement screen
// (RiskAckGate). Sourced verbatim from:
//   - docs/site/legal/risk-disclosure.md, "## Summary (appended to every
//     strategy page)" block — the only paragraph that document marks as
//     reusable outside the full text.
//   - docs/site/copy/onboarding.md, "## Step 2 — Risk disclosure
//     (blocking)" — wizard.risk.confirm / wizard.risk.confirm.help.
//
// {{brand}} / {brand} placeholders are substituted with "Arb Console"
// (the name used everywhere else in this console: web/src/app/layout.tsx
// metadata.title, ConsoleShell's "ARB CONSOLE" wordmark). The Summary's
// trailing "[Risk Disclosure](/legal/risk-disclosure)" markdown link is
// rendered as plain text here, not a link — that route does not exist in
// this console yet; do not ship a dead link.
//
// RISK_DISCLOSURE_VERSION must match the backend's
// app.RiskDisclosureVersion (internal/app/tenancy.go) exactly: the
// backend 409s a risk-ack POST whose version does not equal its current
// one (internal/api/orgapi.go handleRiskAck). Treat this constant as the
// initial value / display fallback only — always prefer the version the
// backend actually asked for (a 403's data.required_version, or
// me.risk_ack_version) when posting the acceptance.
export const RISK_DISCLOSURE_VERSION = "2026-08-27";

export const RISK_DISCLOSURE_TITLE = "Read this before you continue";

// One paragraph per array entry, rendered as separate <p> elements.
export const RISK_DISCLOSURE_SUMMARY: string[] = [
  "Risk summary. Arb Console measures and simulates; it does not trade, hold funds, hold your exchange keys or advise. Spreads, carry and simulated results are measurements net of modelled fees, not predictions.",
  "Many measured spreads cannot be traded: quotes move, depth is thin, transfers are slow or blocked, withdrawal status is unknown, venues fail.",
  "Simulations exclude transfers, assume top-of-book fills, model perpetuals at one-times notional with a hard stop, and use taker fees.",
  "Simulated results are prepared with hindsight and no account has traded them. Nothing on this page is a promise of any outcome. Read the full Risk Disclosure.",
];

export const RISK_ACK_CONFIRM_LABEL =
  "I have read the Risk Disclosure. I understand that Arb Console does not trade, hold funds or advise; that spreads and simulated results are measurements, not predictions; and that no outcome is promised.";

export const RISK_ACK_CONFIRM_HELP =
  "We record the version, the time and your network address as proof of acceptance.";

export const RISK_ACK_DECLINE_LABEL = "I do not accept";

export const RISK_ACK_DECLINED_BODY =
  "Without accepting the Risk Disclosure you cannot continue. Nothing has been saved.";

export const RISK_ACK_CTA = "Accept and continue";
