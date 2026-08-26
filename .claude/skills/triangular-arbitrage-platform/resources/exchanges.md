# Resource: Exchanges

Authoritative documents: `docs/research/exchanges.md` (per-venue survey),
`docs/research/fees.md` (economics + endpoints),
`docs/research/final-platform-selection.md` (decision + scores).

Decisions in force:
- Exchange #1: **Binance** (Demo Mode for development; public market data
  needs no credentials). Exchange #2: **OKX** — only after Binance meets
  SKILL.md §79 definition of done; re-verify docs then.
- Connectors are hand-written Go (see `resources/architecture.md`); CCXT
  only as offline metadata/test oracle.

Binance connector quick facts (verify against current official docs when
implementing — they rotate):
- Feed `<symbol>@depth@100ms` on `wss://data-stream.binance.vision`;
  REST snapshot `/api/v3/depth?limit=<=5000` (weight up to 250; budget
  6000/min/IP); splice per official U/u rule; no checksum.
- 24h forced disconnect → scheduled pre-emptive reconnect; server ping
  20s/pong ≤1min; ≤1024 streams/conn; ≤300 connection attempts/5min.
- Instruments: `/api/v3/exchangeInfo` (PRICE_FILTER / LOT_SIZE /
  NOTIONAL). Fees: `/sapi/v1/asset/tradeFee` +
  `/api/v3/account/commission?symbol=` (BNB discount + promo zero-fee
  pairs). Server time `/api/v3/time`.
- Demo Mode: `https://demo-api.binance.com`,
  `wss://demo-stream.binance.com` — live-equivalent filters, realistic
  data. Testnet (`testnet.binance.vision`) as fallback.

Capability descriptors: every venue integration declares its capabilities
explicitly (init model, integrity model, cadence, depth tiers, fee-asset
convention, precision model, min-notional semantics, test-env kind) in
`internal/exchange` — code must branch on capabilities, never on venue
name strings scattered around.

Credentials policy: public/demo/read-only only; never withdrawal or
transfer permissions; IP allowlisting when available (docs/security.md §2).
