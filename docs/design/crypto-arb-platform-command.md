# The platform command (regenerated 2026-08-27)

This supersedes docs/design/scanner-suite.md §0 as the operator's request of
record. The Scanner Suite (Phase 22) is the first product inside it.

```
/crypto-arb-platform Turn arb-chain-bot into a complete, automated crypto
arbitrage product — comparable in scope to arbitragescanner.io but our own
design, our own packages and our own copy (similar, never identical) — with
these properties:

PRODUCT
1. Scanner Suite (Phase 22): cross-venue spot screener, perpetuals basis and
   funding monitor, spreads calculator, alert rules → Telegram/e-mail/web,
   saved templates, and automatic PAPER execution of every strategy.
2. Triangular engine (existing): intra-venue cycles with the same alerts and
   automatic paper execution.
3. Strategies as first-class objects: cross-venue spot (inventory model),
   spot+futures carry, futures+futures, funding-rate harvesting, triangular;
   each with its own paper ledger, statistics and campaign report.
4. Venues: as many as public data allows, added through one connector
   interface with a conformance test — target Binance, OKX, Bybit, Bitget,
   Gate, MEXC, KuCoin, HTX, Kraken, Coinbase, Crypto.com, Bitfinex, BingX,
   Upbit, Bithumb, WhiteBIT, LBank, BitMart, Phemex, and DEX aggregators
   (Uniswap/PancakeSwap/Jupiter via public quote APIs) — each marked with
   what is verified and what is not.

AUTOMATION
5. Everything runs unattended: collectors, rule evaluation, paper execution,
   nightly campaign reports, health checks, backups, migrations, deploys —
   with alerts on failure. No manual terminal steps in normal operation.
6. Environments: dev (compose), paper-test (staging, paper only), and
   production. Production runs the same code with live execution DISABLED
   until the gate in §"Production execution" below is passed; until then
   production means production-grade hosting of the paper product.

CLIENTS AND PACKAGES
7. Multi-tenant SaaS: organisations, users, roles; packages (e.g. Starter,
   Trader, Pro, Desk, Enterprise — our names, our limits: venues, scanners,
   alert channels, auto-paper strategies, API access, history depth); trial
   period; Paddle billing (subscriptions, upgrades, invoices, webhooks);
   affiliate programme; white-label option as a later phase.
8. Public marketing site (own design and copy): product pages, pricing,
   docs, blog, case studies FROM OUR OWN PAPER REPORTS (no invented client
   results), legal pages (terms, privacy, risk disclosure, refund policy).
9. Client console = the existing operator console re-skinned per tenant
   (layout in the arbitragescanner style: icon sidebar, collapsible product
   groups, filter cards, dense live tables, light/dark); operator/admin
   console for us.

INFRASTRUCTURE
10. Robust infra: Kubernetes (or equivalent) with HA Postgres (managed or
    Patroni), TimescaleDB/partitioning for ticks, Redis for fan-out/rate
    gates, object storage for recordings, CI/CD with staged deploys,
    blue/green or canary, secrets in a KMS-backed vault, backups + tested
    restores, Prometheus/Grafana/Loki/Tempo, SLOs and on-call alerts, WAF and
    rate limiting, DDoS protection, audit logging, GDPR data handling.

TEAM (Claude Code agents, each an expert in crypto trading systems)
11. Product manager, UX designer, UI designer, marketing strategist,
    content copywriter, growth analyst, exchange-connector engineer,
    derivatives quant, infra/SRE engineer, billing engineer (Paddle),
    compliance reviewer, support/docs writer — in addition to the existing
    engineering agents. Skills: crypto-arb-platform (this command),
    venue-connector, saas-billing, prod-infra, marketing-site,
    scanner-suite, console-feature.

RULES
- Money math stays decimal; the risk engine remains the final authority;
  nothing is described as guaranteed; every published number comes from a
  paper report that exists in docs/campaigns/.
- Exchange API keys are only ever stored in the vault; nothing reads them
  until the production gate is passed and the reviewed consumer exists.
- PRODUCTION EXECUTION GATE (live orders with real funds): requires ALL of
  (a) ≥ 30 days of automatic paper execution with positive net PnL after
  fees across calm/volatile/weekend regimes, reported in docs/campaigns/;
  (b) a security review of the execution path and key handling;
  (c) the operator's own legal/regulatory decision recorded in
  docs/decisions/ (client-fund execution is regulated activity in most
  jurisdictions; signals-only SaaS is the default product); (d) an explicit
  code change that replaces ErrLiveTradingDisabled, reviewed and merged by
  a human. Until then LIVE stays disabled in every environment.
```

## Why the gate is not optional

Every measurement so far is negative (triangular on Binance: best gross
+4 bps against 40 bps costs). Automating live orders on a strategy with no
positive paper evidence automates losses; doing it with clients' money
adds regulatory exposure. The product that can ship now is the one
arbitragescanner.io ships — signals + tooling — plus what they do not
have: automatic paper execution that turns signals into evidence. That
evidence is also the marketing material.

## Phasing

- Phase 22 Scanner Suite (in progress): T-065..T-072.
- Phase 23 Venue breadth: connector interface + conformance test; venues
  added in tiers (T-073..T-076).
- Phase 24 Strategies + auto-paper across strategies, nightly reports,
  full automation of operations (T-077..T-080).
- Phase 25 SaaS: tenancy, packages, Paddle billing, affiliate, marketing
  site, client console (T-081..T-088).
- Phase 26 Production infra: k8s, HA Postgres, observability, backups,
  security hardening, staged deploys (T-089..T-094).
- Phase 27 Production execution gate (T-095) — blocked on the gate above.

## Parity review (2026-08-29)

`docs/design/arbitragescanner-parity.md` compares the shipped tree to the
competitor's public feature set. Three amendments to this command follow
from it:

1. **DEX is under-specified here.** Item 4 gives it one clause ("and DEX
   aggregators … via public quote APIs") and the plan carried it as the
   single-line T-076. It is the largest remaining product gap and now has
   a design of record — `docs/design/dex-arbitrage.md` — decomposed into
   T-110..T-116, with its own skill (`dex-arbitrage`) and agent
   (`dex-engineer`). Scope is fixed there: aggregator quote APIs only, no
   wallet keys, no signing, no contract deployment, no mempool, no
   bridging; identity keyed on `(chain_id, contract_address)` and never on
   a symbol; gas as a fixed per-transaction cost that sets each rule's
   `min_notional`; CEX↔DEX as an inventory lane, not a round trip.
   **DEX paper evidence never satisfies the production execution gate on
   its own**, because the paper model omits MEV extraction — the dominant
   adversarial cost on a real swap.

2. **P2P is out, pending an operator decision.** The competitor scans P2P
   fiat corridors; this command never mentioned them. Recommended against
   on the merits (T-103): a P2P advertisement is not an executable quote,
   so the lane cannot produce a campaign report, which breaks the evidence
   rule this whole programme rests on. Any reversal needs a record under
   `docs/decisions/` naming the corridors and the jurisdiction analysis.

3. **Nothing may be sold that is not built.** T-102 (P0, DONE
   2026-08-29): the Desk and Enterprise packages advertised a DEX tier
   with no implementation behind it. Billing was still in Paddle sandbox,
   so nothing had been mis-sold. The operator's decision was to switch the
   capability off until it is built, and the fix is enforced rather than
   merely corrected: `entitlements.DexImplemented` is false, and
   `Validate` refuses any package, stored document or tenant override that
   advertises DEX. Generalised rule for the programme, now with a test
   behind it: an advertised package capability must resolve to something
   that exists in the tree
   (`TestAdvertisedTiersResolveToRegisteredVenues`). Apply the same shape
   to every future capability flag — a boolean that nothing reads is a
   promise nothing keeps.
