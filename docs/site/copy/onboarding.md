---
title: First-run wizard strings
status: DRAFT — compliance-reviewer sign-off required before publish
drafted: 2026-08-27
owner: content-copywriter
constraints: review-2026-08-27 items 3 (versioned risk acceptance), 7 (no default asset-picking templates), 8 (country + consumer/business capture, derivatives gating), 11, 21 (simulated balance label)
format: key = string; keys are i18n ids for the console
---

<!-- No percentage or currency figure. Simulated balances are entered by
     the user; the wizard suggests none. No emojis. -->

## Step 0 — Welcome

```
wizard.welcome.title        = Set up your organisation
wizard.welcome.body         = Five short steps. You can change everything later from Settings.
wizard.welcome.note         = {brand} measures quotes, applies fees, alerts you, and simulates execution on paper. It does not trade, hold funds or take exchange keys.
wizard.welcome.cta          = Begin
```

## Step 1 — Who you are

```
wizard.identity.title       = Where you are and how you use this
wizard.identity.country     = Country of residence
wizard.identity.country.help = We use this to apply the consumer rights and content rules of your country. It cannot be changed without contacting support.
wizard.identity.status      = I use {brand} as
wizard.identity.status.business = a business or professional
wizard.identity.status.consumer = a private individual (consumer)
wizard.identity.status.help = Consumers in some countries have a statutory right of withdrawal and additional protections. Choose honestly; it affects your rights.
wizard.identity.excluded    = {brand} is not available in your country. We are sorry. [COUNSEL: excluded list]
wizard.identity.cta         = Continue
```

## Step 2 — Risk disclosure (blocking)

```
wizard.risk.title           = Read this before you continue
wizard.risk.version         = Risk Disclosure version {version}, effective {date}
wizard.risk.body            = [full text of docs/site/legal/risk-disclosure.md rendered inline, scrollable]
wizard.risk.scroll_hint     = Scroll to the end to enable the confirmation.
wizard.risk.confirm         = I have read the Risk Disclosure. I understand that {brand} does not trade, hold funds or advise; that spreads and simulated results are measurements, not predictions; and that no outcome is promised.
wizard.risk.confirm.help    = We record the version, the time and your network address as proof of acceptance.
wizard.risk.consumer_perf   = I expressly request that the service begins immediately and I acknowledge that I lose my statutory right of withdrawal once it has begun. My separate money-back right under the Refund Policy is unaffected. [COUNSEL: item 11 — show only to consumers; exact wording]
wizard.risk.cta             = Accept and continue
wizard.risk.decline         = I do not accept
wizard.risk.declined.body   = Without accepting the Risk Disclosure the account cannot be created. Nothing has been saved.
```

## Step 3 — Venues

```
wizard.venues.title         = Choose venues
wizard.venues.body          = Your package allows {venues_max} screener venues and {tri_max} triangular venues. Each venue shows whether its fee schedule is verified.
wizard.venues.fee.verified  = fees verified {date}
wizard.venues.fee.unverified = fees unverified — rows on this venue are flagged
wizard.venues.watch_fixed   = The Watch package uses three fixed venues. Upgrade to choose your own.
wizard.venues.perps.title   = Perpetuals data
wizard.venues.perps.body    = The perpetuals monitor is information only. Derivatives may be unavailable to retail clients in your country.
wizard.venues.perps.gated   = Based on your declared country and status, perpetuals rows are shown with an information-only notice and simulated derivatives positions are unavailable. [COUNSEL: item 8]
wizard.venues.perps.toggle  = Show perpetuals data
wizard.venues.cta           = Continue
```

## Step 4 — First rule

```
wizard.rule.title           = Write your first rule
wizard.rule.body            = A rule is a measurement you want to be told about. There are no default rules and we do not suggest assets; every threshold is yours.
wizard.rule.kind            = Rule type
wizard.rule.kind.spread     = Cross-venue spread
wizard.rule.kind.triangular = Triangular cycle
wizard.rule.kind.basis      = Basis (spot vs perpetual)
wizard.rule.kind.funding    = Funding rate
wizard.rule.kind.locked     = Not included in your package
wizard.rule.pair            = Pair or base asset
wizard.rule.venues          = Venues to compare
wizard.rule.threshold       = Net threshold, basis points, after fees
wizard.rule.threshold.help  = Net means after taker fees on every leg. Gross spreads are shown for reference but rules fire on net.
wizard.rule.min_depth       = Minimum top-of-book depth (base units)
wizard.rule.max_age         = Maximum data age (ms)
wizard.rule.cooldown        = Cool-down between alerts (seconds)
wizard.rule.slip            = Slippage allowance (basis points)
wizard.rule.slip.help       = Used only by simulated execution. Realised slippage is measured after the fact and reported next to this allowance.
wizard.rule.skip            = Skip for now
wizard.rule.cta             = Save rule
wizard.rule.saved           = Rule "{rule_name}" saved and enabled.
```

## Step 5 — Alerts

```
wizard.alerts.title         = Where alerts go
wizard.alerts.body          = Your package includes: {channels}. Every alert carries the measurement footer; alerts with paper figures carry the simulated-results footer. Alerts never contain instructions.
wizard.alerts.web           = Web inbox (always on)
wizard.alerts.telegram      = Telegram
wizard.alerts.telegram.help = Open the bot link and press Start; we store your chat id encrypted and never log it. Telegram is a third-country processor; see the Privacy Policy.
wizard.alerts.telegram.connected = Telegram connected.
wizard.alerts.email         = E-mail
wizard.alerts.webhook       = Webhook URL
wizard.alerts.webhook.help  = Payloads are JSON with decimal strings; simulated figures carry "mode": "paper".
wizard.alerts.quota         = Daily alert limit on your package: {per_day}. Events beyond it are stored in the inbox and marked "skipped: quota".
wizard.alerts.locked        = Not included in your package — alerts routed here are delivered to the web inbox instead.
wizard.alerts.cta           = Continue
```

## Step 6 — Simulated ledger

```
wizard.ledger.title         = Simulated ledger
wizard.ledger.body          = Paper execution records what a simulated order would have done, net of modelled fees. Nothing is sent to any venue.
wizard.ledger.name          = Ledger name
wizard.ledger.balance       = Simulated balance per venue (quote asset)
wizard.ledger.balance.help  = This number is not money. Choose a size that resembles what you would actually deploy; results are shown first in basis points of deployed so the balance size does not change how a result looks.
wizard.ledger.size_cap      = Per-execution size cap (quote asset)
wizard.ledger.size_cap.help = Cannot exceed your package cap of {max_size_quote}.
wizard.ledger.attach        = Attach simulated execution to rule "{rule_name}"
wizard.ledger.attach.locked = Simulated execution for this strategy is not included in your package. The rule will alert only.
wizard.ledger.watch         = The Watch package supports manual paper entries only.
wizard.ledger.footer        = Simulated results have inherent limitations; see the Risk Disclosure, section 4.
wizard.ledger.cta           = Create ledger
```

## Step 7 — Done

```
wizard.done.title           = Set up complete
wizard.done.summary         = Organisation {org_name} · package {package_name} · {n_venues} venues · {n_rules} rule · alerts to {channels} · ledger "{ledger_name}" (simulated)
wizard.done.trial           = Your Operator trial ends {trial_end}. No card is on file. At the end you move to Watch; data above the Watch limits is kept 30 days for export.
wizard.done.evidence        = Evidence so far: the only filed report (campaign 01M0ZPK16CXTR91MMJQ60HC2K3) executed no cycles and makes no profitability statement. Your own reports start when your rules fire.
wizard.done.cta             = Open the screener
wizard.done.docs            = Read the docs
```

## Persistent console strings introduced by the wizard

```
banner.simulated            = SIMULATED — paper result, no order sent. Risk Disclosure
banner.info_only            = Information only. Derivatives availability depends on your country and venue.
banner.pending_billing      = Package change pending confirmation from the payment processor.
banner.past_due             = Payment failed. Full access continues for 7 days, then alerts and simulated execution pause until payment succeeds.
label.simulated_balance     = Simulated balance
label.net_bps_first         = Net (bps of deployed)
label.fee_verified          = Fees verified {date}
label.fee_unverified        = Fees unverified
label.data_age              = Data age
label.no_evidence           = No evidence yet
reason.DATA_AGE             = Skipped: a quote was older than allowed
reason.DEPTH                = Skipped: not enough top-of-book depth
reason.ENTITLEMENT          = Skipped: not included in your package
reason.QUOTA                = Skipped: daily alert quota reached
reason.SIZE_CAP             = Size clamped to the package cap
reason.POSITION_OPEN        = Skipped: a simulated position is already open for this rule and base
risk.link                   = Risk Disclosure
risk.reaccept.title         = The Risk Disclosure has changed
risk.reaccept.body          = Version {version} takes effect {date}. Please read and accept it to continue.
```
