// Package paddle integrates Paddle Billing (API v1) for T-083: verified,
// idempotent webhooks that mirror subscriptions into our database,
// server-side checkout transactions for the Paddle.js overlay, customer
// portal sessions, and the upgrade/downgrade/cancel rules from
// docs/design/packages.md §4.
//
// Money facts live in Paddle. We never see a card number: checkout runs
// in Paddle's hosted overlay and payment methods are managed in Paddle's
// customer portal. Entitlements change only when a verified webhook
// says so — never on a client-side redirect.
package paddle

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Subscription is our mirror row (migration 000014).
type Subscription struct {
	OrgID             int64      `json:"org_id"`
	CustomerID        string     `json:"paddle_customer_id,omitempty"`
	SubscriptionID    string     `json:"paddle_subscription_id,omitempty"`
	PriceID           string     `json:"price_id,omitempty"`
	Status            string     `json:"status"`
	CurrentPeriodEnd  *time.Time `json:"current_period_end,omitempty"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
	PastDueSince      *time.Time `json:"past_due_since,omitempty"`
	ScheduledPriceID  string     `json:"scheduled_price_id,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// Paddle subscription statuses (verbatim).
const (
	StatusTrialing = "trialing"
	StatusActive   = "active"
	StatusPastDue  = "past_due"
	StatusPaused   = "paused"
	StatusCanceled = "canceled"
)

// Price is one operator-mapped Paddle price.
type Price struct {
	PriceID     string `json:"price_id"`
	PackageCode string `json:"package_code"`
	Interval    string `json:"billing_interval"` // month | year
}

// Event is a decoded webhook envelope. Data keeps the raw JSON of the
// entity; the typed views below decode the fields we use.
type Event struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
	Raw        []byte          `json:"-"`
}

// SubscriptionData is the subset of a Paddle subscription entity we
// mirror (subscription.* events).
type SubscriptionData struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	CustomerID string `json:"customer_id"`
	Items      []struct {
		Status string `json:"status"`
		Price  struct {
			ID string `json:"id"`
		} `json:"price"`
	} `json:"items"`
	CurrentBillingPeriod *struct {
		StartsAt time.Time `json:"starts_at"`
		EndsAt   time.Time `json:"ends_at"`
	} `json:"current_billing_period"`
	ScheduledChange *struct {
		Action      string     `json:"action"`
		EffectiveAt *time.Time `json:"effective_at"`
	} `json:"scheduled_change"`
	CustomData map[string]any `json:"custom_data"`
}

// TransactionData is the subset of a transaction entity we use
// (transaction.completed): the link to the subscription and the net
// amount for the affiliate ledger.
type TransactionData struct {
	ID             string         `json:"id"`
	SubscriptionID string         `json:"subscription_id"`
	CustomerID     string         `json:"customer_id"`
	CustomData     map[string]any `json:"custom_data"`
	Details        struct {
		Totals struct {
			Total        string `json:"total"`
			Tax          string `json:"tax"`
			Fee          string `json:"fee"`
			Earnings     string `json:"earnings"`
			CurrencyCode string `json:"currency_code"`
		} `json:"totals"`
	} `json:"details"`
}

// PriceOf returns the first active item's price id.
func (d SubscriptionData) PriceOf() string {
	for _, it := range d.Items {
		if it.Status == "" || it.Status == "active" || it.Status == "trialing" {
			return it.Price.ID
		}
	}
	if len(d.Items) > 0 {
		return d.Items[0].Price.ID
	}
	return ""
}

// OrgID reads custom_data.org_id (set by our checkout) as an int64.
func orgIDFrom(custom map[string]any) (int64, bool) {
	v, ok := custom["org_id"]
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case string:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + int64(c-'0')
		}
		return n, n > 0
	case float64:
		return int64(x), x > 0
	}
	return 0, false
}

// Store is the persistence surface (internal/storage.Billing in
// production, MemoryStore in tests).
type Store interface {
	// RecordEvent inserts the event id; fresh=false means it was already
	// fully processed (replay) and must be acknowledged without side
	// effects.
	RecordEvent(ctx context.Context, ev Event) (fresh bool, err error)
	MarkProcessed(ctx context.Context, eventID string, at time.Time) error
	Subscription(ctx context.Context, orgID int64) (Subscription, bool, error)
	SubscriptionByPaddleID(ctx context.Context, subscriptionID string) (Subscription, bool, error)
	UpsertSubscription(ctx context.Context, s Subscription) error
	PackageForPrice(ctx context.Context, priceID string) (packageCode, interval string, ok bool, err error)
	SetPrice(ctx context.Context, priceID, packageCode, interval string) error
	ListPrices(ctx context.Context) ([]Price, error)
}

// Packages is the organisation-side effect of a subscription change:
// the tenancy store's SetPackage plus the resolver's Invalidate.
type Packages interface {
	SetPackage(ctx context.Context, orgID int64, packageCode string, trialEndsAt *time.Time) error
}

var (
	ErrBadSignature   = errors.New("paddle: invalid webhook signature")
	ErrStaleSignature = errors.New("paddle: webhook timestamp outside tolerance")
	ErrUnmappedPrice  = errors.New("paddle: price is not mapped to a package")
	ErrNoCustomer     = errors.New("paddle: organisation has no Paddle customer yet")
	ErrNoSubscription = errors.New("paddle: organisation has no subscription")
	ErrNotConfigured  = errors.New("paddle: PADDLE_API_KEY / PADDLE_WEBHOOK_SECRET not configured")
	ErrBadRequest     = errors.New("paddle: invalid request")
)
