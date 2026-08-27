# Recorded fixtures (T-066)

Every file is one real response from the venue's PUBLIC endpoint, recorded
with curl on 2026-08-27 and trimmed to <= ~52 symbols (a deterministic
subset present in both the instrument list and the bulk ticker, plus a
couple of non-tradable rows so the filters are exercised). No field was
edited; only array elements were dropped.

| venue | file | endpoint |
|---|---|---|
| binance | spot_exchangeInfo.json | GET https://api.binance.com/api/v3/exchangeInfo |
| binance | spot_bookTicker.json | GET https://api.binance.com/api/v3/ticker/bookTicker |
| binance | fut_exchangeInfo.json | GET https://fapi.binance.com/fapi/v1/exchangeInfo |
| binance | fut_bookTicker.json | GET https://fapi.binance.com/fapi/v1/ticker/bookTicker |
| binance | fut_premiumIndex.json | GET https://fapi.binance.com/fapi/v1/premiumIndex |
| binance | fut_fundingInfo.json | GET https://fapi.binance.com/fapi/v1/fundingInfo |
| okx | instruments_spot.json / instruments_swap.json | GET https://www.okx.com/api/v5/public/instruments?instType=SPOT|SWAP |
| okx | tickers_spot.json / tickers_swap.json | GET https://www.okx.com/api/v5/market/tickers?instType=SPOT|SWAP |
| okx | mark_price.json | GET https://www.okx.com/api/v5/public/mark-price?instType=SWAP |
| okx | funding_rate.json | GET https://www.okx.com/api/v5/public/funding-rate?instId=BTC-USDT-SWAP |
| bybit | instruments_spot.json / instruments_linear.json | GET https://api.bybit.com/v5/market/instruments-info?category=spot|linear&limit=1000 |
| bybit | tickers_spot.json / tickers_linear.json | GET https://api.bybit.com/v5/market/tickers?category=spot|linear |
| bitget | symbols.json | GET https://api.bitget.com/api/v2/spot/public/symbols |
| bitget | spot_tickers.json | GET https://api.bitget.com/api/v2/spot/market/tickers |
| bitget | mix_tickers.json | GET https://api.bitget.com/api/v2/mix/market/tickers?productType=USDT-FUTURES |
| bitget | contracts.json | GET https://api.bitget.com/api/v2/mix/market/contracts?productType=USDT-FUTURES |
| bitget | current_fund_rate.json | GET https://api.bitget.com/api/v2/mix/market/current-fund-rate?symbol=BTCUSDT&productType=USDT-FUTURES |
| gate | currency_pairs.json | GET https://api.gateio.ws/api/v4/spot/currency_pairs |
| gate | spot_tickers.json | GET https://api.gateio.ws/api/v4/spot/tickers |
| gate | futures_tickers.json | GET https://api.gateio.ws/api/v4/futures/usdt/tickers |
| gate | contracts.json | GET https://api.gateio.ws/api/v4/futures/usdt/contracts |
| gate | currencies.json | GET https://api.gateio.ws/api/v4/spot/currencies |
| mexc | exchangeInfo.json | GET https://api.mexc.com/api/v3/exchangeInfo |
| mexc | bookTicker.json | GET https://api.mexc.com/api/v3/ticker/bookTicker |
| mexc | contract_ticker.json | GET https://contract.mexc.com/api/v1/contract/ticker |
| mexc | contract_detail.json | GET https://contract.mexc.com/api/v1/contract/detail |
| mexc | funding_rate.json | GET https://contract.mexc.com/api/v1/contract/funding_rate/BTC_USDT |

## T-075 Tier-2 venues (recorded 2026-08-27, same rules: real responses, array elements dropped only)

| venue | file | endpoint |
|---|---|---|
| kucoin | symbols.json | GET https://api.kucoin.com/api/v2/symbols |
| kucoin | allTickers.json | GET https://api.kucoin.com/api/v1/market/allTickers |
| kucoin | currencies.json | GET https://api.kucoin.com/api/v3/currencies |
| kucoin | contracts_active.json | GET https://api-futures.kucoin.com/api/v1/contracts/active |
| kucoin | futures_allTickers.json | GET https://api-futures.kucoin.com/api/v1/allTickers |
| htx | symbols_v2.json | GET https://api.huobi.pro/v2/settings/common/symbols |
| htx | tickers.json | GET https://api.huobi.pro/market/tickers |
| htx | currencies.json | GET https://api.huobi.pro/v2/reference/currencies |
| htx | swap_contract_info.json | GET https://api.hbdm.com/linear-swap-api/v1/swap_contract_info |
| htx | batch_merged.json | GET https://api.hbdm.com/linear-swap-ex/market/detail/batch_merged?business_type=swap |
| htx | swap_batch_funding_rate.json | GET https://api.hbdm.com/linear-swap-api/v1/swap_batch_funding_rate |
| htx | swap_index.json | GET https://api.hbdm.com/linear-swap-api/v1/swap_index |
| htx | mark_price_kline.json | GET https://api.hbdm.com/index/market/history/linear_swap_mark_price_kline?contract_code=BTC-USDT&period=1min&size=1 (served for any contract_code) |
| kraken | AssetPairs.json | GET https://api.kraken.com/0/public/AssetPairs?assetVersion=1 |
| kraken | Ticker.json | GET https://api.kraken.com/0/public/Ticker?assetVersion=1 |
| kraken | futures_instruments.json | GET https://futures.kraken.com/derivatives/api/v3/instruments |
| kraken | futures_tickers.json | GET https://futures.kraken.com/derivatives/api/v3/tickers |
| kraken | historicalfundingrates.json | GET https://futures.kraken.com/derivatives/api/v4/historicalfundingrates?symbol=PF_XBTUSD (last 6 rows; served for any symbol) |
| coinbase | products.json | GET https://api.coinbase.com/api/v3/brokerage/market/products?product_type=SPOT |
| coinbase | product_book_<product_id>.json (36 files) | GET https://api.coinbase.com/api/v3/brokerage/market/product_book?product_id=<product_id>&limit=1 |
| coinbase | exchange_currencies.json | GET https://api.exchange.coinbase.com/currencies |
