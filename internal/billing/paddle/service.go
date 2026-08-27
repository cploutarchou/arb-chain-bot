package paddle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
)

// maxWebhookBody bounds a webhook payload (Paddle events are a few KB).
const maxWebhookBody = 1 << 20

// Service ties the pieces together for the API layer.
type Service struct {
	Store    Store
	Packages Packages
	Client   *Client
	Log      *slog.Logger
	Now      func() time.Time
	// WebhookSecret resolves PADDLE_WEBHOOK_SECRET at call time.
	WebhookSecret func(ctx context.Context) string
	// Invalidate drops the resolver cache for an organisation after a
	// subscription change (nil-safe).
	Invalidate func(orgID int64)
	// OnTransaction is the affiliate-ledger hook (T-084): called once per
	// fresh transaction.completed with the net amount as a decimal
	// string. nil = no affiliate programme wired.
	OnTransaction func(ctx context.Context, orgID int64, transactionID, netUSD string, at time.Time)
	// ClientToken and Environment are the public Paddle.js settings the
	// checkout response carries ("sandbox" | "production"). Not secrets.
	ClientToken string
	Environment string
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Configured reports whether both Paddle secrets resolve.
func (s *Service) Configured(ctx context.Context) bool {
	return s.WebhookSecret != nil && s.WebhookSecret(ctx) != "" && s.Client != nil && s.Client.APIKey != nil && s.Client.APIKey(ctx) != ""
}

// ServeHTTP is the webhook endpoint (paddle:webhooks contract): verify
// the signature over the raw body, dedupe on event_id, apply, then 2xx.
// Any failure after verification returns a 5xx so Paddle retries with
// the same event id; a replay of a processed event is acknowledged with
// no side effects.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err != nil || len(body) == 0 || len(body) > maxWebhookBody {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	secret := ""
	if s.WebhookSecret != nil {
		secret = s.WebhookSecret(r.Context())
	}
	if err := VerifySignature(r.Header.Get("Paddle-Signature"), body, secret, s.now()); err != nil {
		// 401 is fine here: any non-2xx is retried, and a rotated secret
		// recovers on redeploy. Nothing about the body is logged.
		s.log().Warn("paddle webhook rejected", "reason", err.Error())
		http.Error(w, "signature", http.StatusUnauthorized)
		return
	}
	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil || ev.EventID == "" || ev.EventType == "" {
		http.Error(w, "malformed event", http.StatusBadRequest)
		return
	}
	ev.Raw = body
	replayed, err := s.Process(r.Context(), ev)
	if err != nil {
		s.log().Error("paddle webhook processing failed", "event_id", ev.EventID, "type", ev.EventType, "error", err)
		http.Error(w, "processing failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"received":true,"replayed":%v}`, replayed)
}

// Process records and applies one verified event. replayed=true means
// the event id had been fully processed before and nothing ran.
func (s *Service) Process(ctx context.Context, ev Event) (replayed bool, err error) {
	fresh, err := s.Store.RecordEvent(ctx, ev)
	if err != nil {
		return false, err
	}
	if !fresh {
		return true, nil
	}
	if err := s.apply(ctx, ev); err != nil {
		return false, err
	}
	return false, s.Store.MarkProcessed(ctx, ev.EventID, s.now())
}

func (s *Service) apply(ctx context.Context, ev Event) error {
	// Locate the mirror row: by Paddle subscription id first, then by
	// custom_data.org_id (first event for a new organisation).
	var probe struct {
		ID             string         `json:"id"`
		SubscriptionID string         `json:"subscription_id"`
		CustomData     map[string]any `json:"custom_data"`
	}
	_ = json.Unmarshal(ev.Data, &probe)
	subID := probe.SubscriptionID
	if isSubscriptionEvent(ev.EventType) {
		subID = probe.ID
	}
	var cur Subscription
	found := false
	if subID != "" {
		var err error
		cur, found, err = s.Store.SubscriptionByPaddleID(ctx, subID)
		if err != nil {
			return err
		}
	}
	if !found {
		if orgID, ok := orgIDFrom(probe.CustomData); ok {
			var err error
			cur, found, err = s.Store.Subscription(ctx, orgID)
			if err != nil {
				return err
			}
			if !found {
				cur = Subscription{OrgID: orgID}
			}
		}
	}
	if cur.OrgID == 0 {
		// Nothing links this event to an organisation: keep it in
		// paddle_events for the operator, apply nothing.
		s.log().Warn("paddle event has no organisation", "event_id", ev.EventID, "type", ev.EventType)
		return nil
	}
	curPkg, err := s.currentPackage(ctx, cur)
	if err != nil {
		return err
	}
	change, err := Apply(cur, curPkg, ev, s.priceLookup(ctx), s.now())
	if err != nil {
		return err
	}
	if change.UnmappedPrice != "" {
		s.log().Warn("paddle price not mapped to a package; entitlements unchanged", "price_id", change.UnmappedPrice, "org_id", cur.OrgID)
	}
	if change.Sub.Status != "" {
		if err := s.Store.UpsertSubscription(ctx, change.Sub); err != nil {
			return err
		}
	}
	if change.PackageCode != "" || change.ClearTrial {
		pkg := change.PackageCode
		if pkg == "" {
			pkg = curPkg
		}
		if err := s.Packages.SetPackage(ctx, cur.OrgID, pkg, nil); err != nil {
			return err
		}
	}
	if s.Invalidate != nil {
		s.Invalidate(cur.OrgID)
	}
	if change.AffiliateTxnID != "" && s.OnTransaction != nil {
		s.OnTransaction(ctx, cur.OrgID, change.AffiliateTxnID, change.Net, ev.OccurredAt)
	}
	return nil
}

func isSubscriptionEvent(t string) bool {
	return len(t) > len("subscription.") && t[:len("subscription.")] == "subscription."
}

// PackageReader is optional on Store implementations that can report
// the organisation's current package (the pgx store can; the memory
// store keeps it in the fake). Without it the service derives the
// current package from the mirrored price.
type PackageReader interface {
	PackageOf(ctx context.Context, orgID int64) (string, error)
}

func (s *Service) currentPackage(ctx context.Context, cur Subscription) (string, error) {
	if pr, ok := s.Packages.(PackageReader); ok {
		return pr.PackageOf(ctx, cur.OrgID)
	}
	if cur.PriceID != "" {
		if pkg, _, ok, err := s.Store.PackageForPrice(ctx, cur.PriceID); err == nil && ok {
			return pkg, nil
		}
	}
	return entitlements.PackageWatch, nil
}

func (s *Service) priceLookup(ctx context.Context) PriceLookup {
	return func(priceID string) (string, bool) {
		pkg, _, ok, err := s.Store.PackageForPrice(ctx, priceID)
		if err != nil || !ok {
			return "", false
		}
		return pkg, true
	}
}

// CheckoutResult is what the console needs to open the Paddle.js
// overlay (paddle:checkout-web): either a transaction to open, or — for
// an existing subscription — confirmation that the change was requested
// and will land via webhook.
type CheckoutResult struct {
	TransactionID string `json:"transaction_id,omitempty"`
	ClientToken   string `json:"client_token,omitempty"`
	Environment   string `json:"environment,omitempty"`
	// Pending is set for plan changes on an existing subscription: the
	// console shows "pending" until the webhook updates entitlements.
	Pending   bool   `json:"pending"`
	Proration string `json:"proration,omitempty"`
	Package   string `json:"package_code"`
}

// Checkout starts a purchase or a plan change for the organisation.
// The price must be mapped; a new organisation gets a transaction for
// the overlay; an organisation with a live subscription gets a Paddle
// subscription update with the §4 proration mode.
func (s *Service) Checkout(ctx context.Context, orgID int64, currentPackage, priceID, email string) (CheckoutResult, error) {
	pkg, _, ok, err := s.Store.PackageForPrice(ctx, priceID)
	if err != nil {
		return CheckoutResult{}, err
	}
	if !ok {
		return CheckoutResult{}, ErrUnmappedPrice
	}
	sub, found, err := s.Store.Subscription(ctx, orgID)
	if err != nil {
		return CheckoutResult{}, err
	}
	if found && sub.SubscriptionID != "" && (sub.Status == StatusActive || sub.Status == StatusTrialing || sub.Status == StatusPastDue) {
		if sub.PriceID == priceID {
			return CheckoutResult{}, fmt.Errorf("%w: already on this price", ErrBadRequest)
		}
		mode := ProrationFor(currentPackage, pkg)
		if err := s.Client.UpdateSubscriptionItems(ctx, sub.SubscriptionID, priceID, mode); err != nil {
			return CheckoutResult{}, err
		}
		return CheckoutResult{Pending: true, Proration: mode, Package: pkg}, nil
	}
	customerID := ""
	if found {
		customerID = sub.CustomerID
	}
	txn, err := s.Client.CreateTransaction(ctx, priceID, orgID, customerID, email)
	if err != nil {
		return CheckoutResult{}, err
	}
	return CheckoutResult{TransactionID: txn, ClientToken: s.ClientToken, Environment: s.Environment, Package: pkg}, nil
}

// PortalURL mints a customer-portal session for the organisation's
// Paddle customer. The customer id comes from our mirror, never from
// the request (paddle:customer-portal security model).
func (s *Service) PortalURL(ctx context.Context, orgID int64) (string, error) {
	sub, found, err := s.Store.Subscription(ctx, orgID)
	if err != nil {
		return "", err
	}
	if !found || sub.CustomerID == "" {
		return "", ErrNoCustomer
	}
	var ids []string
	if sub.SubscriptionID != "" {
		ids = []string{sub.SubscriptionID}
	}
	return s.Client.CreatePortalSession(ctx, sub.CustomerID, ids)
}

// Cancel schedules cancellation at period end.
func (s *Service) Cancel(ctx context.Context, orgID int64) error {
	sub, found, err := s.Store.Subscription(ctx, orgID)
	if err != nil {
		return err
	}
	if !found || sub.SubscriptionID == "" {
		return ErrNoSubscription
	}
	return s.Client.CancelSubscription(ctx, sub.SubscriptionID)
}

// RunScheduled applies deferred downgrades whose period has ended
// (job path; see ApplyScheduled).
func (s *Service) RunScheduled(ctx context.Context, orgID int64) error {
	sub, found, err := s.Store.Subscription(ctx, orgID)
	if err != nil || !found {
		return err
	}
	next, pkg, changed := ApplyScheduled(sub, s.priceLookup(ctx), s.now())
	if !changed {
		return nil
	}
	if err := s.Store.UpsertSubscription(ctx, next); err != nil {
		return err
	}
	if err := s.Packages.SetPackage(ctx, orgID, pkg, nil); err != nil {
		return err
	}
	if s.Invalidate != nil {
		s.Invalidate(orgID)
	}
	return nil
}

// IsNotConfigured reports the "secrets missing" failure for a 503.
func IsNotConfigured(err error) bool { return errors.Is(err, ErrNotConfigured) }
