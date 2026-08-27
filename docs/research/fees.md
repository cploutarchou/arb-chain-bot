# Fee Analysis — Spot Fees & Instrument Rules (7 Exchanges)

Status: RESEARCHED (official docs/APIs where reachable, accessed 2026-08-26);
RE-VERIFIED 2026-08-26 from a network-enabled host (T-047) — corrections are
marked "[re-verified 2026-08-26]" inline; items still not pinned to a primary
source are explicitly labelled and mirrored in
`final-platform-selection.md` §7.
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
- BNB fee payment: **-25%** → 7.5 bps taker [re-verified 2026-08-26:
  binance.com/en/fee/trading shows Regular 0.100%/0.100% and the "BNB 25%
  off" benefit]. `GET /api/v3/account/commission` exposes a `discount`
  object (`enabledForAccount`, `enabledForSymbol`, `discountAsset`,
  `discount`); per the official Commission FAQ the value is a
  **multiplier applied to the standard commission** (a 25 % rebate
  surfaces as `discount ≈ "0.75000000"`), and the FAQ's worked numbers are
  explicitly fictional — never treat a doc example as the live rate.
  Sources: github.com/binance/binance-spot-api-docs faqs/commission_faq.md
  and rest-api.md (account endpoints), accessed 2026-08-26.
- **Zero-fee promo pairs** [re-verified 2026-08-26 via Binance's public
  announcement CMS (`bapi/composite/v1/public/cms/article/...`, catalogs
  49/93, pages 1–10)]: the promos found and their windows —
  KGST/USDT zero maker+taker for all verified users 2026-06-01 → 2026-08-31
  (article 8313e84b…); BTC/U zero fees 2026-04-17 → 2026-07-16 (expired,
  fe2b0d28…); BTC/JPY & BNB/JPY zero fees VIP2–9 + liquidity providers
  2026-04-01 → 2026-06-30 (expired, 599c19e3…). **The FDUSD program is
  live but is a zero-MAKER promotion** [browser-verified 2026-08-27 on
  binance.com/en/fee/tradingPromote, "Zero Fee" tab, public view]: every
  listed X/FDUSD pair shows maker 0 % and taker "Standard VIP Rates";
  only FDUSD/USDT is 0 %/0 %. The page footnote states that pairs under
  zero-fee promotions are excluded from VIP volume and liquidity
  programs and that BNB discounts and rebates do not apply on them,
  while zero-maker pairs still count. For a taker-only bot the FDUSD
  legs therefore cost the standard taker rate; the "U Promo" and "USDC
  Promo" tabs exist on the same page but were not read (the browser
  session was signed in, so interaction stopped). A BNB/ADA/TRX/XRP–USDC zero-fee promo for **VIP2–9 and
  Spot Liquidity Providers** (2026-08-12 → 2026-10-11) was seen only on
  Binance's official X account, not on a retrievable announcement page —
  secondary evidence; and being VIP2+ it does not apply to a Regular-tier
  bot anyway. Every promo excludes BNB discounts and VIP-volume credit on
  the pair. Conclusion for the engine: **no zero-fee leg is assumed at
  Regular tier in 2026-08**; promo status is a runtime-verified fact pulled
  per symbol from `account/commission` (policy unchanged).
- Fee asset: charged in the **asset received** (buy → base, sell → quote),
  or BNB when BNB-pay enabled.
- Fee APIs: `GET /sapi/v1/asset/tradeFee` (all symbols),
  `GET /api/v3/account/commission?symbol=X` (standard/special/tax + BNB
  discount).
- Instrument rules: `GET /api/v3/exchangeInfo` filters — `PRICE_FILTER`
  (tickSize), `LOT_SIZE` (minQty/stepSize), `NOTIONAL`
  (minNotional/applyMinToMarket), `MARKET_LOT_SIZE`.
- 3-leg taker: **30 bps base / 22.5 bps with BNB / 0 on promo-pair legs**
  (no promo leg in a liquid Regular-tier triangle as of 2026-08-26).

### OKX
- Base tier (Regular user): maker 0.08% / taker 0.10% [re-verified
  2026-08-26 from the server-side JSON embedded in `okx.com/fees`
  (`feeDataInfo.tableData`, Spot/Standard table) and the official
  trading-fee-rules FAQ (`okx.com/help/trading-fee-rules-faq`)].
- **Ladder [re-verified 2026-08-26]**: the fee page served to this host
  (EU regional variant, `currency: EUR`, `showOkb: false`) tiers spot fees
  by **30-day trading volume OR assets on platform**, with **no OKB-holding
  ladder** (`lowerOkbVolume = -1` on every row): Regular 0.08/0.10 %;
  VIP1 0.0675/0.08 % (≥1 M USD 30d vol or ≥100 k assets); VIP2 0.06/0.07 %;
  VIP3 0.055/0.065 %; VIP4 0.03/0.045 %; VIP5 0.025/0.035 %; VIP6 0/0.03 %;
  VIP7 −0.002/0.025 %; VIP8 −0.005/0.02 %; VIP9 −0.005/0.015 %. A separate
  "Stablecoins" spot table exists (subTab 10; not needed for the taker
  bound). Regional variants remain — the platform still reads
  `/api/v5/account/trade-fee` at runtime when keys exist; keyless
  deployments assume Regular tier. The round-1 "OKB ladder" research debt
  is therefore CLOSED: the current schedule has no OKB tier.
- Fee asset: deducted from the **currency received** (buy → base, sell →
  quote) [re-verified 2026-08-26: the trading-fee-rules FAQ example —
  "bought 1 BTC … fee = 0.1% × 1 = 0.001 BTC … receive 0.999 BTC";
  "sold 1 BTC … received 20,000 USDT … fee = 16 USDT"]. → `FeeInReceived`.
- Fee API: `GET /api/v5/account/trade-fee?instType=SPOT[&instId=...]`
  (5 req/2 s per UserID; response carries `level`, `maker`, `taker` as
  negative-signed decimals e.g. `"taker": "-0.001"`, plus `feeGroup`
  rows) [docs-v5, accessed 2026-08-26].
- Instrument rules: `GET /api/v5/public/instruments?instType=SPOT` →
  `tickSz`, `lotSz`, `minSz` (base size; **no min-notional field for spot**).
- Promo/zero-fee pairs: none found on the fee page or docs on 2026-08-26.
- 3-leg taker: **30 bps base** (Regular); VIP1 24 bps needs ≥1 M USD/30 d.

### Bybit
- Base tier: spot 0.10%/0.10%; crypto-fiat schedule starts 0.15%/0.20%.
- MNT fee-payment discount (-25%) **excludes API-executed trades**
  [re-verified 2026-08-26: bybit.com help "FAQ – Paying Trading Fees with
  MNT": "not supported for … API users … API orders"]; the 2026 USDC
  retail-taker promo is **manual trades only**. A bot pays full rate.
- Fee asset: charged in the **asset received**.
- Fee API: `GET /v5/account/fee-rate?category=spot[&symbol=...]`.
- Instrument rules: `GET /v5/market/instruments-info?category=spot` →
  `priceFilter.tickSize`; `lotSizeFilter.basePrecision` (qty step),
  **`minOrderAmt` (min notional in quote — the enforced minimum;
  `minOrderQty` deprecated)**.
- 3-leg taker: **30 bps for a bot** (fiat legs 60 bps).

### Kraken (Kraken Pro)
- Base tier: **maker 0.40% / taker 0.80% (40/80 bps)** [re-verified
  2026-08-26 — CORRECTED from round 1's 25/40]. On 2026-07-09 Kraken moved
  to a cross-platform "best of" tiering (spot 30-day volume, futures
  volume, or assets on platform) and Tier 1 is now 0.40%/0.80%
  (support.kraken.com/articles/cross-platform-fee-tier-changes;
  kraken.com/features/fee-schedule, accessed 2026-08-26).
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
- 3-leg taker: **240 bps base — economically non-viable for taker cycles**
  (was 120 bps under the pre-2026-07-09 schedule); all-stablecoin cycle
  60 bps (stable/FX schedule unchanged at 0.20%/0.20%). FOK time-in-force
  added to AddOrder on 2026-05-12 (docs.kraken.com change log).

### Coinbase Advanced Trade
- Base tier (Intro, <$1K 30d): maker 0.60% / taker 1.20% (60/120 bps);
  $1K–$10K ≈ 0.35%/0.75%. [re-verification 2026-08-26/27: the tier table
  is behind a login — help.coinbase.com/…/advanced-trade-fees says "sign
  in … and see the Coinbase Advanced fees page", and in a browser
  coinbase.com/advanced-fees redirects straight to the sign-in page
  (verified 2026-08-27). Figures stay secondary-sourced and unchanged;
  confirm via `transaction_summary`.] Institutional Coinbase Exchange
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
  not supported by official pages.) [2026-08-26: gate.com and gate.io doc
  and help domains return HTTP 403 to non-browser clients from this host
  too; figures unchanged, still not re-pinned to a primary page.]
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
| OKX | 10 | 10 (no OKB ladder; VIP1 8 needs ≥1 M USD/30 d) | 30 / 30 | Y (per-pair) |
| Bybit | 10 | 10 for bots (MNT excludes API) | 30 / 30 (fiat 60) | Y (per-pair) |
| Kraken | 80 (Tier 1 since 2026-07-09) | 80 | 240 / 240 (all-stable 60) | Y (per-pair) |
| Coinbase Adv. | 120 | 120 | 360 / 360 | Y (tier only) |
| Bitget | 10 | 8 (BGB) | 30 / **24** | Y (per-pair) |
| Gate.io | 20 | 15 (GT) | 60 / 45 | Y (per-pair) |

## Consequences for the platform

1. **Viability boundary.** Referenced against the constraints analysis
   (typical liquid-triangle deviations 0–10 bps), only Binance (22.5 bps
   with BNB, plus zero-fee promo legs), Bitget (24 bps with BGB), and Bybit
   (30 bps flat) are near the taker-cycle frontier at base tier. Kraken and
   Coinbase Advanced are non-viable for 3-leg taker cycles without high
   volume tiers, regardless of their API quality. Kraken's 2026-07-09
   re-tiering (Tier 1 taker 0.80 %) doubles its 3-leg cost to 240 bps.
2. **Promo pairs are strategy-defining — when they exist.** Binance
   triangles routing legs through zero-fee promo pairs can drop the fee
   floor to one paid leg (≤10 bps) or less; as of 2026-08-26 the only
   Regular-tier zero-fee spot pair found is KGST/USDT (to 2026-08-31),
   which is not in any liquid triangle. The fee engine must support per-symbol
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

Re-verification sources (accessed 2026-08-26): binance.com/en/fee/trading;
raw.githubusercontent.com/binance/binance-spot-api-docs (rest-api.md,
web-socket-streams.md, filters.md, faqs/commission_faq.md,
demo-mode/general-info.md); binance.com public CMS announcement API
(article codes cited inline); okx.com/fees embedded `feeDataInfo` JSON;
okx.com/help/trading-fee-rules-faq; okx.com/docs-v5/en (Get fee rates);
support.kraken.com/articles/cross-platform-fee-tier-changes;
kraken.com/features/fee-schedule; docs.kraken.com change log;
bybit.com help-center FAQ-Paying-Trading-Fees-with-MNT;
help.coinbase.com advanced-trade-fees.

Round-1 source URLs per exchange are retained in the research record; primary
ones: github.com/binance/binance-spot-api-docs (rest-api.md, filters.md),
developers.binance.com trade-fee, binance.com fee FAQs & promo announcements,
okx.com/fees + docs-v5, github.com/bybit-exchange/docs (fee-rate.mdx,
instrument.mdx), bybit.com fee help/announcements, kraken.com fee schedule +
support 360039299431 + docs.kraken.com (AssetPairs, TradeVolume, AddOrder),
coinbase.com/legal/trading_rules + docs.cdp.coinbase.com (transaction_summary,
products), bitget.com support 12560603820584 + 360060644351 + api-doc
(Get-Symbols, Get-Trade-Rate), gate.com/fee + github.com/gateio/gateapi-python
(CurrencyPair, TradeFee, WalletApi).
