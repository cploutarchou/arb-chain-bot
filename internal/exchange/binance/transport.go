package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

// RESTClient is the minimal public-data REST surface: depth snapshots,
// instrument metadata, server time. No credentials — public market data
// only (docs/security.md §2).
type RESTClient struct {
	Base string
	HC   *http.Client
}

func NewRESTClient(base string) *RESTClient {
	return &RESTClient{Base: base, HC: &http.Client{Timeout: 10 * time.Second}}
}

func (c *RESTClient) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	u := c.Base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HC.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// 429/418 carry Retry-After; the caller's backoff must honor it.
		return nil, &HTTPError{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After"), Body: truncate(body, 256)}
	}
	return body, nil
}

// HTTPError surfaces rate-limit responses distinctly.
type HTTPError struct {
	Status     int
	RetryAfter string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("binance: HTTP %d (retry-after %q): %s", e.Status, e.RetryAfter, e.Body)
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

// Depth fetches a snapshot for the syncer splice. Weight at limit 5000 is
// 250 — callers pace within the 6000/min budget.
func (c *RESTClient) Depth(ctx context.Context, symbol exchange.Symbol, limit int) (orderbook.DepthEvent, error) {
	q := url.Values{"symbol": {string(symbol)}, "limit": {fmt.Sprint(limit)}}
	body, err := c.get(ctx, "/api/v3/depth", q)
	if err != nil {
		return orderbook.DepthEvent{}, err
	}
	return DecodeRESTSnapshot(symbol, body, time.Now())
}

// ExchangeInfo fetches and maps instrument metadata.
func (c *RESTClient) ExchangeInfo(ctx context.Context) ([]exchange.Market, error) {
	body, err := c.get(ctx, "/api/v3/exchangeInfo", nil)
	if err != nil {
		return nil, err
	}
	return ParseExchangeInfo(body)
}

// ServerTime returns the venue clock plus the request round-trip, for the
// clock manager's offset estimate.
func (c *RESTClient) ServerTime(ctx context.Context) (server time.Time, rtt time.Duration, err error) {
	start := time.Now()
	body, err := c.get(ctx, "/api/v3/time", nil)
	if err != nil {
		return time.Time{}, 0, err
	}
	rtt = time.Since(start)
	var out struct {
		ServerTime int64 `json:"serverTime"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return time.Time{}, 0, err
	}
	return time.UnixMilli(out.ServerTime), rtt, nil
}

// StreamURL builds the combined-streams URL for depth-diff subscriptions
// at 100ms cadence.
func StreamURL(host string, symbols []exchange.Symbol) string {
	parts := make([]string, 0, len(symbols))
	for _, s := range symbols {
		parts = append(parts, strings.ToLower(string(s))+"@depth@100ms")
	}
	return host + "/stream?streams=" + strings.Join(parts, "/")
}
