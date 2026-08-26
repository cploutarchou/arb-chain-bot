# Fee Analysis — Spot Fees & Instrument Rules (7 Exchanges)

Status: RESEARCHED (official docs/APIs where reachable, accessed 2026-08-26)
Purpose: determine the effective 3-leg taker cost per venue — the qualification
bar every triangle must clear — and catalogue the fee/instrument-rule APIs the
fee engine must consume.

Method note: verified primarily via official GitHub-hosted API docs (Binance,
Bybit, Gate fetched raw) and search reads of official fee/help pages (OKX,
Kraken, Coinbase, Bitget). Items not pinned to an official page are marked
UNVERIFIED. Fees rotate with promos — the platform must re-pull each account's
actual rates from the per-account fee endpoints below at runtime, never
hardcode them.

---

## Per-exchange findings

### Binance
- Base tier (VIP0): maker 0.10% / taker 0.10% (10/10 bps).
- BNB fee payment: **-25%** → 7.5 bps taker; confirmed in API:
  `GET /api/v3/account/commission` returns `discount: {discountAsset: "BNB",
  discount: "0.25"}`.
- **Zero-fee promo pairs active in 2026**: selected FDUSD pairs
  (BTC/BNB/DOGE/ETH/LINK/SOL/XRP–FDUSD) for Regular+VIP1; a USDC zero-fee
  promotion; BNB/USDC and other launches. Volatile — confirm per symbol via
  `account/commission`.
- Fee asset: charged in the **asset received** (buy → base, sell → quote),
  or BNB when BNB-pay enabled.
- Fee APIs: `GET /sapi/v1/asset/tradeFee` (all symbols),
  `GET /api/v3/account/commission?symbol=X` (standard/special/tax + BNB
  discount).
- Instrument rules: `GET /api/v3/exchangeInfo` filters — `PRICE_FILTER`
  (tickSize), `LOT_SIZE` (minQty/stepSize), `NOTIONAL`
  (minNotional/applyMinToMarket), `MARKET_LOT_SIZE`.
- 3-leg taker: **30 bps base / 22.5 bps with BNB / 0 on promo-pair legs**.

### OKX
- Base tier (Regular Lv1): maker 0.08% / taker 0.10%.
- No pay-in-token rebate; regular-user ladder set by **OKB holdings** (exact
  Lv2–Lv5 rates UNVERIFIED — read `/api/v5/account/trade-fee`). Fee framework
  restructured 2025-11-25; regional variants exist.
- Fee asset: deducted from the **currency received** (buy → base, sell →
  quote); optional quote-currency payment for buys.
- Fee API: `GET /api/v5/account/trade-fee?instType=SPOT[&instId=...]`.
- Instrument rules: `GET /api/v5/public/instruments?instType=SPOT` →
  `tickSz`, `lotSz`, `minSz` (base size; **no min-notional field for spot**).
- 3-leg taker: **30 bps base**; lower with OKB tiers (UNVERIFIED).

### Bybit
- Base tier: spot 0.10%/0.10%; crypto-fiat schedule starts 0.15%/0.20%.
- MNT fee-payment discount (-25%) **excludes API-executed trades**; the 2026
  USDC retail-taker promo is **manual trades only**. A bot pays full rate.
- Fee asset: charged in the **asset received**.
- Fee API: `GET /v5/account/fee-rate?category=spot[&symbol=...]`.
- Instrument rules: `GET /v5/market/instruments-info?category=spot` →
  `priceFilter.tickSize`; `lotSizeFilter.basePrecision` (qty step),
  **`minOrderAmt` (min notional in quote — the enforced minimum;
  `minOrderQty` deprecated)**.
- 3-leg taker: **30 bps for a bot** (fiat legs 60 bps).

### Kraken (Kraken Pro)
- Base tier (<$10K 30d volume): maker 0.25% / taker 0.40% (25/40 bps).
  Since 2026-07-09 tier is 30-day volume OR assets-on-platform.
- No token discount. Separate **stablecoin/FX schedule**: flat 0.20%/0.20%
  when the *base* is a stablecoin/FX (USDT/USD, EUR/USD…); crypto pairs
  quoted in stablecoins use the normal schedule. USDG pairs 0%/0.01%.
  Active maker-rebate program on 650+ pairs (2026-08-05).
- Fee asset: default is the currency **spent** — buys default fee-in-quote
  (`fciq`), sells fee-in-base (`fcib`), overridable per order.
- Fee APIs: private `TradeVolume?pair=` (actual per-pair fees); public
  `AssetPairs` embeds per-pair tier arrays `fees`/`fees_maker`.
- Instrument rules: `GET /0/public/AssetPairs` → `ordermin` (min base qty),
  `costmin` (min notional), `tick_size`, `pair_decimals`, `lot_decimals`.
- 3-leg taker: **120 bps base — economically non-viable for taker cycles**;
  all-stablecoin cycle 60 bps.

### Coinbase Advanced Trade
- Base tier (Intro, <$1K 30d): maker 0.60% / taker 1.20% (60/120 bps);
  $1K–$10K ≈ 0.35%/0.75%. (help.coinbase.com unreachable from this
  environment; high-confidence secondary reads of the official page —
  confirm via `transaction_summary`.) Institutional Coinbase Exchange
  (0.40%/0.60%) is a different product; comparison sites conflate them.
- No token discount. Stable-pair schedule: maker 0%, taker ~0.001–0.0045%
  (exact current retail value UNVERIFIED).
- Fee asset: charged in the **quote asset** (Coinbase Markets Trading Rules).
- Fee API: `GET /api/v3/brokerage/transaction_summary` (account tier only —
  **no per-pair fee endpoint**).
- Instrument rules: `GET /api/v3/brokerage/products` → `base_increment`,
  `quote_increment`, `base_min_size`, `quote_min_size`.
- 3-leg taker: **360 bps base — prohibitive**.

### Bitget
- Base tier: 0.10%/0.10%.
- **BGB fee deduction: -20%** → 8 bps taker (falls back to normal if BGB
  balance insufficient). Applies to API trading (no API exclusion found).
- No standing zero-fee spot pairs verified; campaigns rotate.
- Fee asset: deducted from the **received asset** (buy → base, sell →
  quote) unless BGB deduction enabled.
- Fee APIs: per-symbol `makerFeeRate`/`takerFeeRate` in
  `GET /api/v2/spot/public/symbols`; account rate via
  `GET /api/v2/common/trade-rate`; VIP table via
  `GET /api/v2/spot/market/vip-fee-rate`.
- Instrument rules: `GET /api/v2/spot/public/symbols` → `pricePrecision` /
  `quantityPrecision` (decimals-based, not step-based), `minTradeAmount`,
  **`minTradeUSDT`** (min notional in USDT), `status`.
- 3-leg taker: **30 bps base / 24 bps with BGB**.

### Gate.io (gate.com)
- Base tier (VIP0): 0.20%/0.20% (20/20 bps). (Third-party claims of 0.10%
  not supported by official pages.)
- **GT deduction ~-25%** at VIP0 (official pages say "10–25%"; the API
  returns exact discounted rates: `gt_maker_fee`/`gt_taker_fee`).
- Fee asset: per-fill `fee` + `fee_currency` (+`gt_fee`); buy→base /
  sell→quote observed default, explicit statement not retrievable —
  confirm from fills.
- Fee APIs: `GET /api/v4/wallet/fee?currency_pair=X`; per-pair default in
  `GET /api/v4/spot/currency_pairs` (`fee` field).
- Instrument rules: `GET /api/v4/spot/currency_pairs` → `amount_precision`,
  `precision` (decimals-based), `min_base_amount`, `min_quote_amount`,
  `trade_status`.
- 3-leg taker: **60 bps base / 45 bps with GT**.

---

## Comparative table (regular account, taker, API-usable discounts only)

| Exchange | Base taker (bps) | Discounted taker (bps) | 3-leg cost bps (base / discounted) | Per-account fee API |
|---|---|---|---|---|
| Binance | 10 | 7.5 (BNB) | 30 / **22.5** (0 on promo legs) | Y (per-pair) |
| OKX | 10 | 10 (OKB tiers lower, UNVERIFIED) | 30 / ~30 | Y (per-pair) |
| Bybit | 10 | 10 for bots (MNT excludes API) | 30 / 30 (fiat 60) | Y (per-pair) |
| Kraken | 40 | 40 | 120 / 120 (all-stable 60) | Y (per-pair) |
| Coinbase Adv. | 120 | 120 | 360 / 360 | Y (tier only) |
| Bitget | 10 | 8 (BGB) | 30 / **24** | Y (per-pair) |
| Gate.io | 20 | 15 (GT) | 60 / 45 | Y (per-pair) |

## Consequences for the platform

1. **Viability boundary.** Referenced against the constraints analysis
   (typical liquid-triangle deviations 0–10 bps), only Binance (22.5 bps
   with BNB, plus zero-fee promo legs), Bitget (24 bps with BGB), and Bybit
   (30 bps flat) are near the taker-cycle frontier at base tier. Kraken and
   Coinbase Advanced are non-viable for 3-leg taker cycles without high
   volume tiers, regardless of their API quality.
2. **Promo pairs are strategy-defining.** Binance triangles routing legs
   through zero-fee FDUSD/USDC promo pairs can drop the fee floor to one
   paid leg (≤10 bps) or less. The fee engine must support per-symbol
   overrides sourced from `account/commission`, refreshed periodically, with
   promo expiry treated as config change (audited).
3. **Discount modeling must be conditional.** BNB/BGB/GT discounts apply
   only when the balance exists and the toggle is on; Bybit's MNT discount
   must NOT be modeled for API flow. The fee engine models: rate source
   (account API), payment asset, discount eligibility, and fee-in-kind
   (received-asset fees reduce the quantity entering the next leg; Kraken's
   spent-side default and Coinbase's quote-side fees change leg math).
4. **Min-notional semantics differ** and the instrument-rules layer must
   normalize them: Binance `NOTIONAL.minNotional`, Bybit `minOrderAmt`
   (quote), Kraken `costmin`+`ordermin`, Coinbase `quote_min_size`+
   `base_min_size`, Bitget `minTradeUSDT`, Gate `min_quote_amount`+
   `min_base_amount`, OKX `minSz` only (base size, no spot min-notional).
5. **Precision models differ**: step/tick-based (Binance, OKX, Bybit,
   Kraken) vs decimals-based (Bitget, Gate). The normalized InstrumentRules
   model must express both without loss.

Full source URLs per exchange are retained in the research record; primary
ones: github.com/binance/binance-spot-api-docs (rest-api.md, filters.md),
developers.binance.com trade-fee, binance.com fee FAQs & promo announcements,
okx.com/fees + docs-v5, github.com/bybit-exchange/docs (fee-rate.mdx,
instrument.mdx), bybit.com fee help/announcements, kraken.com fee schedule +
support 360039299431 + docs.kraken.com (AssetPairs, TradeVolume, AddOrder),
coinbase.com/legal/trading_rules + docs.cdp.coinbase.com (transaction_summary,
products), bitget.com support 12560603820584 + 360060644351 + api-doc
(Get-Symbols, Get-Trade-Rate), gate.com/fee + github.com/gateio/gateapi-python
(CurrencyPair, TradeFee, WalletApi).
