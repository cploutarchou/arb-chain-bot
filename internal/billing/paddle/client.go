package paddle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// API base URLs (Paddle Billing API v1).
const (
	SandboxAPI = "https://sandbox-api.paddle.com"
	LiveAPI    = "https://api.paddle.com"
)

// Client is the thin server-side Paddle API client. The API key is
// resolved per call from the secrets manager so a rotation applies
// immediately, and it is never logged.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// APIKey resolves the key at call time ("" = not configured).
	APIKey func(ctx context.Context) string
}

func NewClient(baseURL string, apiKey func(ctx context.Context) string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 15 * time.Second}, APIKey: apiKey}
}

// APIError is a non-2xx Paddle response (body truncated, never logged
// with the request).
type APIError struct {
	Status int
	Code   string
	Detail string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("paddle: api %d %s: %s", e.Status, e.Code, e.Detail)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	key := ""
	if c.APIKey != nil {
		key = c.APIKey(ctx)
	}
	if key == "" {
		return ErrNotConfigured
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Paddle-Version", "1")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("paddle: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var env struct {
			Error struct {
				Code   string `json:"code"`
				Detail string `json:"detail"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		detail := env.Error.Detail
		if len(detail) > 200 {
			detail = detail[:200]
		}
		return &APIError{Status: resp.StatusCode, Code: env.Error.Code, Detail: detail}
	}
	if out != nil {
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

// CreateTransaction creates a checkout transaction for one price. The
// returned id is what Paddle.js opens: Paddle.Checkout.open({
// transactionId }). custom_data.org_id is how the webhook finds the
// organisation; the customer email pre-fills checkout.
func (c *Client) CreateTransaction(ctx context.Context, priceID string, orgID int64, customerID, email string) (transactionID string, err error) {
	body := map[string]any{
		"items":       []map[string]any{{"price_id": priceID, "quantity": 1}},
		"custom_data": map[string]any{"org_id": fmt.Sprint(orgID)},
	}
	if customerID != "" {
		body["customer_id"] = customerID
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/transactions", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// UpdateSubscriptionItems replaces the subscription's item with priceID
// using the given proration_billing_mode (ProrationFor). Payment failure
// on an immediate proration prevents the change (the user keeps the old
// plan; the console shows the error) rather than creating a dangling
// past_due transaction.
func (c *Client) UpdateSubscriptionItems(ctx context.Context, subscriptionID, priceID, prorationMode string) error {
	body := map[string]any{
		"items":                  []map[string]any{{"price_id": priceID, "quantity": 1}},
		"proration_billing_mode": prorationMode,
		"on_payment_failure":     "prevent_change",
	}
	return c.do(ctx, http.MethodPatch, "/subscriptions/"+subscriptionID, body, nil)
}

// CancelSubscription schedules the cancellation for the end of the
// current period (packages.md §4: immediate only by support).
func (c *Client) CancelSubscription(ctx context.Context, subscriptionID string) error {
	return c.do(ctx, http.MethodPost, "/subscriptions/"+subscriptionID+"/cancel",
		map[string]any{"effective_from": "next_billing_period"}, nil)
}

// CreatePortalSession mints a one-time customer portal URL.
func (c *Client) CreatePortalSession(ctx context.Context, customerID string, subscriptionIDs []string) (url string, err error) {
	body := map[string]any{}
	if len(subscriptionIDs) > 0 {
		body["subscription_ids"] = subscriptionIDs
	}
	var out struct {
		URLs struct {
			General struct {
				Overview string `json:"overview"`
			} `json:"general"`
		} `json:"urls"`
	}
	if err := c.do(ctx, http.MethodPost, "/customers/"+customerID+"/portal-sessions", body, &out); err != nil {
		return "", err
	}
	return out.URLs.General.Overview, nil
}
