# LBank — public endpoint research (T-075)

Scope: PUBLIC (no API key) endpoints only, spot first (the contract
host is not usable from this environment — see §3). Access date for
every citation: **2026-09-13**. Legend as in
docs/research/screener-endpoints.md (VERIFIED / VERIFIED-ABSENT /
UNVERIFIED). Planned collector: internal/screener/venue/lbank.go.

## 1. Spot bulk ticker (no bid/ask) + per-symbol depth
- `GET /v2/ticker/24hr.do?symbol=all` → `[{symbol:"btc_usdt", ticker:
  {high, vol, low, change, turnover, latest}, timestamp (ms)}, …]` —
  every pair in ONE request; **no bid/ask and no sizes** (fields are
  the 24 h rolls plus `latest`). **VERIFIED** (live; the parameter is
  `symbol=all` — `symbolAll=1`, `symbol_all=true` and no-arg all
  return `{"result":"false","error_code":10001,"msg":"Parameter can
  not be null"}`, live-verified)
- `GET /v2/depth.do?symbol=btc_usdt&size=1` (size 1–60) →
  `data:{asks:[["price","qty"]…], bids:[…], timestamp}` — the ONLY
  top-of-book source, **per symbol**. Arrays sorted best-first.
  **VERIFIED** (live)
- Consequence (design): LBank needs a bounded per-symbol depth sweep
  under the rate gate (see §5); the collector sweeps
  BooksPerPoll pairs round-robin and carries every other pair's
  last-known quote with its own timestamp (the Coinbase pattern) —
  untouched pairs age out through the normal data-age gates, never
  fabricated from `latest`. A turnover-ranked sweep would starve dust
  pairs forever; round-robin covers everything on a slower cycle.

## 2. Spot instruments / constraints
- `GET /v2/currencyPairs.do` → `["btc_usdt", …]` (lowercase
  `base_quote`). **VERIFIED** (live)
- `GET /v2/accuracy.do?symbol=btc_usdt` →
  `[{symbol, quantityAccuracy:"5", minOrderAmount:"1",
  minTranQua:"0.00001", priceAccuracy:"2"}]` — real tick/step/min-size
  constraints per pair. **VERIFIED** (live)

## 3. USDT-M perpetuals
- The contract API host is `https://lbkperp.lbank.com` (per ccxt's
  `urls.contract`); `GET
  /cfd/open/api/v1/pub/market/data?productGroup=SwapU` there answers
  `{"error_code":10006,"msg":"not open path"}` and
  `/open/api/v1/pub/market/data` 404s (openresty). The spot host
  (`api.lbkex.com`) serves HTML 404s for the same paths. The swap
  market-data path this environment can exercise is not identified;
  the docs site (www.lbank.com/docs) is a JS app that does not render
  in a fetch. **UNVERIFIED — perps deferred; the first collector ships
  spot-only**

## 4. Currency / chain status — PUBLIC
- No public deposit/withdrawal-status endpoint in the v2 surface
  (withdrawal configs are authenticated). **VERIFIED-ABSENT**

## 5. Rate limits / bans
- The official docs page is unreachable (JS app, §3); ccxt encodes a
  20 ms polite-poll default (`rateLimit: 20`) which is a client
  convention, not the venue's number. **UNVERIFIED**
- Collector gates (plan): assume a conservative 10 requests/s per IP
  for depth sweeps — 1 bulk ticker + a rotating depth window (e.g. 60
  symbols/poll at 1 s poll) stays at ~61 req/s-equivalent bursts only
  if swept continuously; the gate must pace the sweep to its budget
  and the docs row must flag the number unverified.

## 6. Fees (regular tier)
- Support article (support.lbank.site…Trading-Fees…) returns 403 from
  this environment; the docs carry no public fee endpoint. **UNVERIFIED
  — the fee table starts from the common base-tier 0.10 %/0.10 % and
  the settings UI shows LBank's fees as unverified**

## 7. Notes
- Envelope: `{"result":"true"|"false","msg","error_code","ts","data"}`.
- Symbols are lowercase with underscore; the collector upcases to the
  repo's `BASE/QUOTE` convention.
- Connector complexity: HIGH (no bulk top-of-book; per-symbol depth
  under an unverified rate budget; spot-only first cut).
