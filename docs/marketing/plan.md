# Marketing plan — evidence-first launch

Status: PROPOSAL 2026-08-27 (marketing-strategist). Owner: marketing-strategist;
sign-off: operator (positioning, budget, KPI targets) and compliance-reviewer
(every public surface, affiliate terms, geo gating) before anything ships.
Anchors: docs/design/crypto-arb-platform-command.md (§CLIENTS AND PACKAGES 8,
RULES), docs/design/packages.md (§1, §2 "Returns and evidence", §5, §7),
docs/compliance/review-2026-08-27.md (items 4–8, 12, 13, 19, 24),
docs/design/strategy-models.md §8, docs/MASTER_PLAN.md Phases 22–27.

This document is **internal**. It contains competitor names and prices for
scope comparison only (compliance item 24): none of it is published, and no
public page makes a comparative claim.

## 0. Evidence state on the day of writing

| Fact | Source |
|---|---|
| One campaign report exists. It executed **zero cycles** and states "no profitability statement can be made from it". | docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3/campaign-01M0ZPK16CXTR91MMJQ60HC2K3.md |
| Every triangular measurement to date is negative (best gross +4 bps against ~40 bps costs). | docs/design/crypto-arb-platform-command.md "Why the gate is not optional" |
| Scanner Suite collectors, spreads math, alert rules and auto-paper are TODO (T-066..T-071); the 24 h soak report is not filed. | docs/MASTER_PLAN.md Phase 22 |
| Live execution is impossible in code (`ErrLiveTradingDisabled`) and not for sale in any package. | packages.md §3.1, §7 |

Consequence for marketing: **there is no performance figure to publish.**
Every public number today is either a capability/limit (from packages.md §2,
once the operator confirms it) or a live screener measurement labelled as
such (compliance item 13). The phrase of record for every strategy is
"no evidence yet". This is not a weakness to hide; it is the positioning.

## 1. Positioning

**One-line.** The only arbitrage product that executes every signal on paper
and publishes the net-of-fees result — including when the result is nothing.

**Category.** Market data, analytics, alerts and simulated (paper) execution
for crypto arbitrage. Not a bot, not a broker, not an adviser. No package
places orders, holds funds or takes exchange keys (packages.md §7).

**Why we win (the axis competitors do not sell).** Scope reference in
packages.md §1: arbitragescanner.io, Cryptohopper and Bitsgap sell signals,
bots or both. None of them automatically paper-execute every signal with
net-of-fee scoring and file a report. Our tiers, our site and our content are
built on that evidence pipeline (scanner-suite.md §1, §4). The claim is about
*what we measure and publish*, never about *what the user will earn*.

**Positioning statement (internal).** For traders and small desks who are
tired of screenshots of spreads that were never tradeable, [product] is a
scanner suite that paper-executes every alert it raises and reports the
result after modelled fees — so you can see, before risking anything, which
spreads survive costs and which do not. Unlike signal services, we publish
the losses too.

**Tone.** Measurement language. "Observed", "net of modelled fees",
"simulated", "sample size", "no evidence yet". No hype, no urgency, no
lifestyle. The evidence dashboard (T-080) is the hero image, not a Lambo.

## 2. Ideal customer profiles

| | ICP-1 Own-funds trader | ICP-2 Small desk | ICP-3 Tooling buyer |
|---|---|---|---|
| Who | Individual trading own capital across 2–6 CEX, already pays or has paid for a scanner or bot | 2–12 people running arbitrage/carry/funding strategies with shared capital; has a Telegram ops channel and a spreadsheet of fees | Quant/dev or data buyer who wants normalised spreads, funding and basis via API/exports to feed their own systems |
| Pain we solve | "The spread was gone when I clicked." Wants to know which alert types actually survive fees before spending time on them | Needs evidence to allocate: which venue pair, which strategy, what drawdown; needs seats, roles, history and per-strategy reports | Needs history depth, exports, rate limits, streaming; does not want to build 20 collectors |
| Package fit (packages.md §2) | Signal → Operator | Desk | Operator (read API) → Desk / Institution |
| Where they are | YouTube arbitrage tutorials, Telegram signal groups, X, Reddit r/CryptoCurrency and r/algotrading | Referral, LinkedIn, conference side-rooms, Telegram desk chats | GitHub, Hacker News, r/algotrading, API docs search |
| First value moment | First weekly report on their own rules: "X alerts fired, Y paper-executed, net Z bps after fees, N too small to conclude" | Per-strategy comparison report; evidence dashboard vs gate thresholds | API key created, first CSV/Parquet export |
| Objection | "Paper is not real" | "Your evidence is thin" | "Data age and coverage" |
| Honest answer | Correct: paper is a filter, not a return. The disclaimer says so. It still tells you which spreads were fictional | Correct: we show sample size and regime coverage against strategy-models.md §8, and say "not enough" when it is | Per-venue verified/unverified flags and data-age fields are in the product |

Not an ICP: anyone looking for "passive income", anyone asking us to trade
for them, retail users in jurisdictions where derivatives content is
restricted (§8) for the perps/funding surfaces.

## 3. Messaging pillars and banned claims

### 3.1 Pillars

1. **Every signal is tested, on paper, automatically.** Alerts are not the
   product; the report that says what the alert would have done is.
2. **Net of fees, or it does not count.** Both taker fees, funding, unwind
   cost; buffers; the failed-cycle line. (strategy-models.md §8 item 3.)
3. **We publish the misses.** The first report executed zero cycles and we
   say so. Sample size and regime coverage are shown against the gate
   thresholds; "not enough evidence" is a valid published state.
4. **Your rules, your measurements.** The user configures filters; the
   product measures. No buy/sell verbs, no default asset-picking templates
   (compliance item 7).
5. **Nothing leaves your exchange.** No exchange keys, no funds, no orders,
   in any package (packages.md §7).

### 3.2 Mandatory elements on every public surface

- Hypothetical-performance disclaimer (CFTC 4.41-style) wherever a
  spread, carry, bps or paper PnL appears, with the model's exclusions:
  no transfers, top-of-book fills, unknown withdrawal status, 1× perps
  with hard stop (compliance item 5).
- Risk disclosure link: untradeable spreads, liquidation/counterparty
  risk, exchange failure, fees-only modelling (item 6).
- "Simulated" label on every paper figure; bps/percent shown before
  currency; "simulated balance" on any balance (item 21).
- Every %/currency figure carries a `docs/campaigns/<id>` citation or
  the build lint fails (item 4). Watch-tier live numbers are labelled
  "live screener sample, 30 s refresh, not a campaign result" (item 13).

### 3.3 Banned (copy lint list, also §5 affiliate rule 5)

"guaranteed", "risk-free", "passive income", "harvest" (use "funding-rate
carry" / "funding strategy"), "earn", "profit from", "returns of", "make
money", "%/day", "%/month", "ROI" without a report citation, "proven",
"safe", "sure thing", "signal to buy/sell", "we trade for you",
"institutional-grade" (unverifiable), any competitor name or price, any
client testimonial about performance, any invented case study, countdown
timers, "limited seats", incentives to sign up (cash-back, bonuses — UK
regime, §8). Case studies exist only as verbatim excerpts of a
docs/campaigns/ report with its id in the title.

## 4. Package narrative (from packages.md §2, capabilities only)

The tiers are named after what the user does, not what they earn:
**Watch** (look at delayed live samples), **Signal** (get alerted, weekly
report on your own rules), **Operator** (auto-paper carry + triangular,
nightly reports, read API), **Desk** (all five strategies, seats, 400 days
of history, per-strategy comparison), **Institution** (custom cadence,
exports to object storage, named contact).

Copy pattern per tier: *who it is for → what it measures → what it
does not do* (never places orders). Prices come from Paddle previews only
(packages.md §4). The 14-day Operator trial without a card is the funnel
step for ICP-1 and ICP-2; Watch is the live demo for the site, not a
product (packages.md §2 rationale). Nothing in the narrative implies
that a higher tier changes the regulatory posture or unlocks live
execution (§7).

## 5. Competitor acquisition research (official pages, accessed 2026-08-27)

Used to size scope and to design *different* mechanics. Nothing here is
quoted publicly.

| Product | Page | What they do to acquire users |
|---|---|---|
| arbitragescanner.io | https://arbitragescanner.io/ | "Get a free trial day" as the primary CTA (repeated 7+ times), entry via a Telegram bot; Telegram channel + support bot, X, Discord, Instagram, Medium; YouTube webinar ("70 use cases of our software"); blog + tutorials + "Cases" (client success stories); private client chat "where are experienced arbitrators"; personal managers; giveaways of bot access; media-logo wall and third-party reviews. |
| arbitragescanner.io affiliate | https://arbitragescanner.io/affiliate | "50% of all their sales" (home page: "up to 50% of each sale and 10% of WhiteLabel sales"); recruits "owners of major networks on YouTube, Telegram, TikTok"; partner events (Bangkok); terms via Telegram contact; cookie window, threshold and payout method not stated. |
| arbitragescanner.io blog | https://arbitragescanner.io/blog | Categories: Earning Strategy (dominant), Research and Analysis, News, Cases, Review. Near-daily cadence. Titles such as "Crypto Funding Rate Arbitrage: Complete Strategy Guide 2026", "How to Calculate Your REAL Arbitrage Profit: A Full Cost Breakdown", "Triangular Arbitrage Explained". |
| Cryptohopper | https://www.cryptohopper.com/ and https://www.cryptohopper.com/affiliate-program | Free tier ("Free to use - no credit required"), marketplace of third-party signals/strategies/templates, internal chat, testimonials ("changed my life"), example trades with "+8888.88%". Affiliate: "Earn 10%, 12.5% or 15% per sale", lifetime, tiered by monthly commission; multi-level (50% of a sub-affiliate's rate, per docs); "high-converting landing pages", "Free Content", partner support; payout threshold $75, requested twice a month, commissions appear after 4–6 weeks (support/docs pages linked from the search results). Pitches affiliation as "passive income powerhouse". |
| Bitsgap | https://bitsgap.com/ and https://bitsgap.com/affiliate-program | "7-day PRO plan trial. No credit card required.", Demo mode "live market simulation without risking your funds", Telegram news channel, YouTube reviews by named partner channels (19K–36K subscribers), TrustPilot/Capterra ratings, headline claims ("11% Average profit 30d return — Grid Bot", "$203M Total one-year bot profit"). Affiliate: "30% commission", cookie "30 days ... automatically renews", "minimum payout amount is €25 ... within 21 days", paid in USDT TRC-20, monthly affiliate competitions. |

What we take from this: a no-card trial, a Telegram community, YouTube
walkthroughs and an educational blog are table stakes. What we
deliberately do differently: no "cases" from clients, no profit headlines,
no giveaways, no affiliate competitions, no crypto payouts, no multi-level,
no partner events funded from commission, no Telegram-bot-first funnel
(sign-up is on our site with country capture and risk acknowledgement). Our
blog cadence is weekly and every post that shows a number cites a report.

## 6. Launch phases (tied to MASTER_PLAN Phases 22–25)

Each phase lists what may be said honestly. A claim moves to the next
column only when the referenced artefact exists in the tree.

| Phase | Ships | What we can market | What we cannot say yet |
|---|---|---|---|
| **Now (Phase 22 in progress)** | Triangular engine, operator console, one zero-cycle report; screener TODO | Build-in-public: the methodology (strategy-models.md §8 gate, fee model), the zero-cycle report as the first "we publish misses" post, waitlist for the Operator trial. Topic content that contains no numbers of ours. | Any spread, hit-rate or PnL; any screener screenshot as if live; a launch date. |
| **Phase 22 done (T-066..T-071)** | Screener, perps/funding monitor, calculator, alert rules → Telegram, auto-paper for spot/carry/futures-futures, 24 h soak report under docs/campaigns/screener/ | "Screener + paper evidence": Watch-tier live sample on the site (labelled), the soak report verbatim, walkthrough video of the report. Status of every strategy: "no evidence yet" unless the soak says otherwise, and a 24 h soak is never described as evidence of profitability. | Any comparison to competitor coverage; any "works" claim. |
| **Phase 23 (T-073..T-076)** | Tier-1 then Tier-2 venues, DEX quotes, verified/unverified flags | Venue coverage pages ("N venues, of which M fee-verified"), per-venue data pages for SEO, "what is unverified and why" post. | "All exchanges", "most venues"; a venue that is not in the registry. |
| **Phase 24 (T-077..T-080)** | Strategy registry, nightly reports per strategy, evidence dashboard vs gate thresholds | Public evidence page fed by docs/campaigns/<strategy>/<date>/; weekly "evidence digest" post and video; per-strategy status badges (no evidence / insufficient sample / negative / positive-in-sample) each linked to its report. This is the core content engine. | Extrapolation ("annualised" from < 30 days), cherry-picked windows, regime-selective quoting; anything about live results. |
| **Phase 25 (T-081..T-088)** | Tenancy, packages, Paddle, affiliate ledger, marketing site, client console | Public launch: pricing from Paddle previews, 14-day Operator trial, affiliate programme opens **after** compliance sign-off of §5 terms (packages.md §7), legal pages live, client reports on the user's own rules. | Live execution as roadmap, upgrade path or "coming soon" (packages.md §7: a separate product and legal decision). SLA claims before Phase 26. |
| **Phase 26–27** | Prod infra; gate | Uptime/SLO statements only once measured (compliance item 25). Nothing about Phase 27 is marketed at all; if the gate ever passes, a new compliance review precedes any mention. | — |

## 7. Channel plan

### 7.1 SEO (owned; primary long-term channel)

Cluster around questions a buyer asks *before* trusting a signal, and
around data we actually have.

- Methodology cluster (evergreen, no numbers of ours required): "how to
  calculate net cross-exchange spread with both taker fees", "why an
  arbitrage alert disappears before you can trade it", "spot-perp basis
  and funding carry, net of fees, explained", "triangular arbitrage costs:
  a worked example at 40 bps", "what a paper-trading report should
  contain", "sample size for arbitrage strategies (Wilcoxon, bootstrap)".
- Venue cluster (Phase 23): one page per venue — public endpoints used,
  fee tier assumed, verified/unverified items, data age — plus "funding
  interval and settlement by venue".
- Evidence cluster (Phase 24): auto-generated per-strategy, per-week
  pages from reports; the disclaimer and exclusions are part of the
  template.
- Glossary: bps, basis, funding interval, top-of-book, unwind, matched
  pair, regime.
- Rule: no article uses a %/currency figure of ours without a report
  citation; competitor names never appear; titles never promise earnings.

### 7.2 Telegram community

One public channel (announcements + weekly evidence digest + report
links) and one discussion group with rules pinned: no signals traded as
advice, no performance screenshots from members, no referral links except
approved affiliates with disclosure. Moderation script from the banned
list in §3.3. Chat IDs handled per compliance item 10. Bot only for
announcements; the sign-up funnel stays on the site (country capture, risk
acknowledgement).

### 7.3 YouTube

Walkthroughs of paper reports, not of trades: "reading a campaign report",
"why this week executed zero cycles", "setting a rule and watching it
paper-execute", "evidence dashboard vs the production gate", "what the fee
model excludes". Disclaimer card in the first 10 seconds and in the
description; no thumbnails with currency figures; comments moderated to
the same rules. Length 6–12 minutes, one per week from Phase 22 done.

### 7.4 Affiliate programme (packages.md §5, with compliance controls)

Terms (proposal, unchanged from packages.md §5): 20 % of net revenue for
12 months then 10 %; Institution 8 % first year; last click, 45-day cookie,
lock at organisation creation; $100 threshold; 45-day maturation; monthly
payout on the 15th; bank/PayPal via Paddle; insert-only decimal ledger;
fraud rules (1)–(6) and clawback 180 days.

Marketing-side controls added for compliance item 12 and §8:

- Enrolment: any paying organisation (Signal+) or approved publisher;
  sanctions screening at enrolment and at every payout; W-8/W-9 collected;
  DAC7/1099 reporting owned by billing.
- **Pre-approval of material**: every landing page, video, post or
  Telegram message that mentions us is submitted and approved before
  publication; approved assets get an id; unapproved live material =
  commission hold, repeat = termination.
- Prohibited-claims list = §3.3 verbatim, plus: no screenshots of paper
  PnL without the disclaimer, no "I made X with this", no comparisons
  with named competitors, no coupon/discount claims (we issue none).
- Disclosure: "affiliate link" or equivalent adjacent to every link; FTC
  Endorsement Guides / ASA style; affiliates never present as us.
- Geo: affiliates may not target UK residents with cryptoasset
  promotions unless the promotion has been approved under the UK regime
  (§8); affiliate dashboard exposes the geo rules and the promotion status.
- Approved assets we supply: a text description of the product (no
  numbers), the evidence page link, the disclaimer block, logo files,
  screenshots of the report template with "simulated" watermarks.
- Opens to the public only after compliance sign-off of the terms
  (packages.md §7). Until then: closed pilot with ≤ 10 publishers.

### 7.5 Partnerships

- Venue education programmes and API developer directories (listing as a
  data/analytics tool; no revenue terms).
- Newsletters and podcasts in the quant/algotrading space for methodology
  pieces (the fee-model and sample-size posts travel well).
- Universities / trading clubs for the paper product as a teaching tool
  (Watch + trial), a clean fit for "simulated" framing.
- No paid "review" placements; no partner events funded from commission.

### 7.6 Paid

Not in the first 12 weeks. Crypto ad policies (Google, Meta, X) require
certification and often geo restrictions; revisit after Phase 25 with
compliance, search-only, non-financial-promotion keywords (methodology
cluster), never on perps terms in UK/US.

## 8. Geo and compliance gating (compliance items 7, 8, 19, 20)

| Jurisdiction | Constraint | Marketing rule |
|---|---|---|
| **UK** | Retail crypto derivatives ban (FCA PS20/10); cryptoasset financial-promotion regime (s21 approver or exemption, prescribed risk warning, 24 h cooling-off for first-time investors, ban on incentives to invest such as refer-a-friend bonuses); marketing pages may be a financial promotion | Perps/funding/basis pages, videos and posts are hidden from UK visitors (geo + self-declared country) or shown as "information only, not available to UK retail"; no UK-targeted promotions until an approver or exemption is recorded in docs/decisions/; no sign-up incentives anywhere (already none); affiliates may not target UK; if in doubt, geo-block sign-up from UK until counsel answers item 19. |
| **US** | Offshore perps not available to US persons; CTA/CPO questions for perps advice; FTC endorsement guides for affiliates; state auto-renewal laws | Perps/funding surfaces shown to US visitors with "information only; these instruments may not be available to US persons"; no per-venue "you can trade this" wording; affiliate disclosures mandatory; auto-renewal disclosure on pricing and checkout. |
| **EU/EEA** | MiCA advice boundary; MiFID for derivatives; UCPD/DSA for affiliates and auto-renewal; 14-day withdrawal right | Outputs framed as user-configured measurements (no buy/sell verbs); consumer/B2B status captured; express consent to immediate performance at checkout; affiliate disclosures. |
| **UAE** | VARA advisory and marketing rules; ADGM/DIFC/SCA | No UAE-targeted campaigns until counsel answers item 17. |
| **Sanctioned jurisdictions** | — | Sign-up, affiliate enrolment and payouts blocked. |

Mechanics: country captured at sign-up and stored; geo lookup on the site
selects the perps-content variant; every perps row carries the
"information only" notice regardless of geo; content calendar items
tagged `perps` get the gated variant by default.

## 9. Content calendar — first 12 weeks

Starts the week Phase 22 collectors run (T-066). Everything before that is
the "Now" column of §6. Every "number" item is blocked on the report id it
cites; if the report is late, the item slips — never substitutes.

| Week | Blog (SEO) | Video | Telegram | Other |
|---|---|---|---|---|
| 1 | "Why we publish the report that executed zero cycles" (cites 01M0ZPK16CXTR91MMJQ60HC2K3) | Reading a campaign report (walkthrough of the zero-cycle report) | Channel opens; pinned rules; digest #1 | Waitlist page live with risk disclosure |
| 2 | Net cross-exchange spread, both taker fees, worked example (no figures of ours) | — | Digest #2 | Methodology piece pitched to two newsletters |
| 3 | Why the alert disappears: lifetime, top-of-book, data age | Setting a rule and watching it paper-execute (paper-test env) | Digest #3 | Glossary published |
| 4 | Spot-perp basis and funding carry net of fees (gated `perps`) | — | Digest #4 | Venue page template ready |
| 5 | The 24 h Scanner Suite soak: what it did and did not show (cites docs/campaigns/screener/ soak) | Walkthrough of the soak report | Digest #5 + report link | Watch-tier live sample on site (labelled) |
| 6 | Triangular costs at 40 bps: the whole stack | — | Digest #6 | Venue pages: Binance, OKX, Bybit |
| 7 | What the fee model excludes (and why that matters) | The fee model and its exclusions | Digest #7 | Venue pages: Bitget, Gate, MEXC |
| 8 | Sample size for arbitrage: Wilcoxon, bootstrap, regimes (strategy-models.md §8) | — | Digest #8 | Closed affiliate pilot invites (≤ 10) |
| 9 | Verified vs unverified: how we flag venue data | Evidence dashboard vs the production gate (T-080, if shipped; else slips) | Digest #9 | Tier-2 venue pages as they land |
| 10 | Funding intervals by venue (gated `perps`) | — | Digest #10 | Evidence page template from nightly reports (Phase 24) |
| 11 | Weekly evidence digest #1 as a post (cites docs/campaigns/<strategy>/<date>/) | Why this week's carry sample is "insufficient" (or whatever the report says) | Digest #11 | Pricing page copy review with compliance (capabilities only) |
| 12 | "Twelve weeks of paper: everything we measured" (all citations) | Twelve-week evidence recap | Digest #12 | Phase 25 launch readiness check: legal pages, lint, geo gating, affiliate sign-off |

Digest format: strategies × status badge (no evidence / insufficient
sample / negative / positive-in-sample) × report link × disclaimer. If no
report ran that week, the digest says so.

## 10. KPI targets and funnel definitions

Targets are internal planning numbers for the first 12 weeks after the
Phase 25 launch (earlier phases track only the top of the funnel). They
are not published and are not evidence of anything. Denominators are
defined so the growth analyst can compute them from the site analytics,
the `subscriptions` mirror and the `affiliate_ledger`.

| Stage | Definition | 12-week target |
|---|---|---|
| Visitors | unique sessions on site (consented analytics only) | 12,000 |
| Engaged | session ≥ 60 s or ≥ 2 pages, or a report/evidence page view | 30 % of visitors |
| Sign-ups | organisation created (country captured, risk acknowledgement accepted) | 4 % of visitors (≈ 480) |
| Activated | ≥ 1 alert rule created and ≥ 1 auto-paper execution or manual paper cycle in first 7 days | 55 % of sign-ups |
| Report opened | user opened their first weekly/nightly report | 45 % of sign-ups |
| Trial → paid | active paid subscription within 21 days of sign-up (`subscription.activated`) | 8 % of trials (≈ 38 paid) |
| Mix | Signal : Operator : Desk | 50 : 40 : 10 |
| 30-day retention | paid org still active at day 30 | ≥ 85 % |
| Money-back / refund rate | refunds ÷ first payments | ≤ 6 % |
| Telegram | channel members / group members | 800 / 300 |
| YouTube | subscribers; median watch-through on report walkthroughs | 500; ≥ 40 % |
| SEO | methodology + venue pages indexed; non-brand clicks/week by week 12 | 30 pages; 400 |
| Affiliates | approved affiliates; share of paid from affiliates; material rejection rate | 10 (pilot); ≤ 20 %; tracked (a high rate is a training problem, not a target) |
| Compliance | lint failures reaching production; unapproved affiliate assets found live | 0; 0 |

Leading indicators reviewed weekly: report-open rate (is the evidence the
product?), activation (is the trial long enough?), and the share of
sign-ups from gated jurisdictions (is geo gating working?). Targets are
revised only with data, and never by loosening a compliance control.

## 11. Open decisions for the operator

1. Confirm package names and prices (packages.md §2) so capability copy
   can be drafted — copy still contains no prices (Paddle previews).
2. Product name and domain (nothing above assumes one).
3. UK: geo-block sign-up outright, or "information only" with an
   approver decision recorded in docs/decisions/ (compliance item 19).
4. Budget for video production and the closed affiliate pilot.
5. Whether the paper-test environment can be shown on video before
   Phase 26 hardening (it is paper-only, but it is still our infra).
