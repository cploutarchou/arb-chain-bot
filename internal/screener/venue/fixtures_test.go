package venue

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// fixtureRoutes maps a venue to (path[+query fragment] → fixture file).
// A route key containing "?" must match the request path and every
// listed query pair (an empty value matches any value); the first match
// in declaration order wins. A file name containing "{<param>}" has the
// request's query value for <param> substituted.
var fixtureRoutes = map[screener.Venue][][2]string{
	screener.VenueBinance: {
		{"/api/v3/exchangeInfo", "spot_exchangeInfo.json"},
		{"/api/v3/ticker/bookTicker", "spot_bookTicker.json"},
		{"/fapi/v1/exchangeInfo", "fut_exchangeInfo.json"},
		{"/fapi/v1/ticker/bookTicker", "fut_bookTicker.json"},
		{"/fapi/v1/premiumIndex", "fut_premiumIndex.json"},
		{"/fapi/v1/fundingInfo", "fut_fundingInfo.json"},
	},
	screener.VenueOKX: {
		{"/api/v5/public/instruments?instType=SPOT", "instruments_spot.json"},
		{"/api/v5/public/instruments?instType=SWAP", "instruments_swap.json"},
		{"/api/v5/market/tickers?instType=SPOT", "tickers_spot.json"},
		{"/api/v5/market/tickers?instType=SWAP", "tickers_swap.json"},
		{"/api/v5/public/mark-price", "mark_price.json"},
		{"/api/v5/public/funding-rate", "funding_rate.json"},
	},
	screener.VenueBybit: {
		{"/v5/market/instruments-info?category=spot", "instruments_spot.json"},
		{"/v5/market/instruments-info?category=linear", "instruments_linear.json"},
		{"/v5/market/tickers?category=spot", "tickers_spot.json"},
		{"/v5/market/tickers?category=linear", "tickers_linear.json"},
	},
	screener.VenueBitget: {
		{"/api/v2/spot/public/symbols", "symbols.json"},
		{"/api/v2/spot/market/tickers", "spot_tickers.json"},
		{"/api/v2/mix/market/tickers", "mix_tickers.json"},
		{"/api/v2/mix/market/contracts", "contracts.json"},
		{"/api/v2/mix/market/current-fund-rate", "current_fund_rate.json"},
	},
	screener.VenueGate: {
		{"/spot/currency_pairs", "currency_pairs.json"},
		{"/spot/tickers", "spot_tickers.json"},
		{"/futures/usdt/tickers", "futures_tickers.json"},
		{"/futures/usdt/contracts", "contracts.json"},
		{"/spot/currencies", "currencies.json"},
	},
	screener.VenueMEXC: {
		{"/api/v3/exchangeInfo", "exchangeInfo.json"},
		{"/api/v3/ticker/bookTicker", "bookTicker.json"},
		{"/api/v1/contract/ticker", "contract_ticker.json"},
		{"/api/v1/contract/detail", "contract_detail.json"},
		{"/api/v1/contract/funding_rate/", "funding_rate.json"},
	},
	screener.VenueKuCoin: {
		{"/api/v2/symbols", "symbols.json"},
		{"/api/v1/market/allTickers", "allTickers.json"},
		{"/api/v3/currencies", "currencies.json"},
		{"/api/v1/contracts/active", "contracts_active.json"},
		{"/api/v1/allTickers", "futures_allTickers.json"},
	},
	screener.VenueHTX: {
		{"/v2/settings/common/symbols", "symbols_v2.json"},
		{"/market/tickers", "tickers.json"},
		{"/v2/reference/currencies", "currencies.json"},
		{"/linear-swap-api/v1/swap_contract_info", "swap_contract_info.json"},
		{"/linear-swap-ex/market/detail/batch_merged", "batch_merged.json"},
		{"/linear-swap-api/v1/swap_batch_funding_rate", "swap_batch_funding_rate.json"},
		{"/linear-swap-api/v1/swap_index", "swap_index.json"},
		{"/index/market/history/linear_swap_mark_price_kline", "mark_price_kline.json"},
	},
	screener.VenueKraken: {
		{"/0/public/AssetPairs", "AssetPairs.json"},
		{"/0/public/Ticker", "Ticker.json"},
		{"/derivatives/api/v3/instruments", "futures_instruments.json"},
		{"/derivatives/api/v3/tickers", "futures_tickers.json"},
		{"/derivatives/api/v4/historicalfundingrates", "historicalfundingrates.json"},
	},
	screener.VenueCoinbase: {
		{"/api/v3/brokerage/market/products", "products.json"},
		// One recorded book per product: "{product_id}" is substituted
		// from the request's product_id query value.
		{"/api/v3/brokerage/market/product_book?product_id=", "product_book_{product_id}.json"},
		{"/currencies", "exchange_currencies.json"},
	},
	screener.VenueCryptoCom: {
		{"/public/get-instruments", "instruments.json"},
		{"/public/get-tickers", "tickers.json"},
		// One recorded valuation per (type, instrument): the instrument
		// name is substituted from the request's query value.
		{"/public/get-valuations?valuation_type=mark_price", "val_mark_{instrument_name}.json"},
		{"/public/get-valuations?valuation_type=funding_hist", "val_funding_{instrument_name}.json"},
		{"/public/get-valuations?valuation_type=estimated_funding_rate", "val_est_{instrument_name}.json"},
	},
	screener.VenueBitfinex: {
		{"/v2/conf/", "conf.json"},
		{"/v2/tickers", "tickers.json"},
		{"/v2/status/deriv", "deriv_status.json"},
	},
	screener.VenueBingX: {
		{"/openApi/spot/v1/common/symbols", "spot_symbols.json"},
		{"/openApi/spot/v2/quote/bookTicker", "spot_bookTicker.json"},
		{"/openApi/swap/v2/quote/contracts", "contracts.json"},
		{"/openApi/swap/v2/quote/ticker", "swap_ticker.json"},
		{"/openApi/swap/v2/quote/premiumIndex", "premiumIndex.json"},
	},
	screener.VenueWhiteBIT: {
		{"/api/v4/public/markets", "markets.json"},
		{"/api/v1/public/tickers", "tickers_v1.json"},
		{"/api/v4/public/futures", "futures.json"},
		{"/api/v4/public/assets", "assets.json"},
	},
	screener.VenueBitMart: {
		{"/spot/v1/symbols/details", "symbols_details.json"},
		{"/spot/quotation/v3/tickers", "tickers_v3.json"},
		{"/spot/v1/currencies", "currencies.json"},
		{"/contract/public/details", "contract_details.json"},
		{"/contract/public/funding-rate-v2", "funding_rate_v2.json"},
		// Served for any symbol (the collector round-robins mark price).
		{"/contract/public/markprice-kline", "markprice_kline.json"},
	},
}

// fixtureServer serves testdata/<venue>/ for one venue and counts
// requests per route so tests can assert call budgets.
type fixtureServer struct {
	*httptest.Server
	hits map[string]int
}

func newFixtureServer(t *testing.T, id screener.Venue) *fixtureServer {
	t.Helper()
	dir := filepath.Join("..", "testdata", string(id))
	fs := &fixtureServer{hits: map[string]int{}}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, route := range fixtureRoutes[id] {
			path, query, _ := strings.Cut(route[0], "?")
			if !strings.HasPrefix(r.URL.Path, path) {
				continue
			}
			if query != "" {
				k, v, _ := strings.Cut(query, "=")
				if got := r.URL.Query().Get(k); got != v && (v != "" || got == "") {
					continue
				}
			}
			fs.hits[route[0]]++
			file := route[1]
			if i := strings.Index(file, "{"); i >= 0 {
				j := strings.Index(file, "}")
				param := file[i+1 : j]
				file = file[:i] + r.URL.Query().Get(param) + file[j+1:]
			}
			b, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				t.Errorf("fixture %s: %v", route[1], err)
				w.WriteHeader(500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
			return
		}
		t.Errorf("%s: unrouted request %s", id, r.URL.String())
		http.NotFound(w, r)
	}))
	t.Cleanup(fs.Close)
	return fs
}

func fixtureCollector(t *testing.T, id screener.Venue) (Collector, *fixtureServer) {
	t.Helper()
	fs := newFixtureServer(t, id)
	// Real clock: the per-venue gates are live in tests too, so a
	// collector that over-spends its window waits instead of hanging on
	// a frozen clock.
	c, err := New(id, Options{SpotBase: fs.URL, PerpBase: fs.URL, FundingCallsPerPoll: 3, BooksPerPoll: 40, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	return c, fs
}
