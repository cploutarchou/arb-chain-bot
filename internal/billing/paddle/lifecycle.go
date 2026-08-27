package paddle

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
)

// Change is the organisation-side outcome of applying one event: the
// new mirror row and, when the package changes, the code to store.
type Change struct {
	Sub            Subscription
	PackageCode    string // "" = unchanged
	ClearTrial     bool   // first paid subscription ends the trial clock
	UnmappedPrice  string // price with no billing_prices row (flagged, not guessed)
	AffiliateTxnID string // transaction.completed id for the ledger hook
	Net            string // transaction earnings (net of tax and Paddle fee)
}

// PriceLookup resolves a price id to a package code.
type PriceLookup func(priceID string) (packageCode string, ok bool)

// Apply is the pure state machine (packages.md §4): given the current
// mirror row (zero value when none), the organisation's current package
// and one event, it returns the next state. It is deterministic and
// convergent — the same event applied twice yields the same row — so
// webhook retries and out-of-order deliveries are safe.
func Apply(cur Subscription, curPackage string, ev Event, prices PriceLookup, now time.Time) (Change, error) {
	out := Change{Sub: cur}
	switch ev.EventType {
	case "subscription.created", "subscription.updated", "subscription.activated", "subscription.trialing",
		"subscription.past_due", "subscription.paused", "subscription.resumed", "subscription.canceled":
		var d SubscriptionData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return out, fmt.Errorf("%w: subscription data: %v", ErrBadRequest, err)
		}
		if d.ID == "" || d.Status == "" {
			return out, fmt.Errorf("%w: subscription data missing id/status", ErrBadRequest)
		}
		// Convergence: an older event must not roll back a newer state.
		if !cur.UpdatedAt.IsZero() && ev.OccurredAt.Before(cur.UpdatedAt) && cur.SubscriptionID == d.ID {
			return out, nil
		}
		sub := cur
		if sub.OrgID == 0 {
			if id, ok := orgIDFrom(d.CustomData); ok {
				sub.OrgID = id
			}
		}
		sub.SubscriptionID = d.ID
		if d.CustomerID != "" {
			sub.CustomerID = d.CustomerID
		}
		sub.Status = d.Status
		sub.UpdatedAt = ev.OccurredAt
		if d.CurrentBillingPeriod != nil {
			end := d.CurrentBillingPeriod.EndsAt
			sub.CurrentPeriodEnd = &end
		}
		sub.CancelAtPeriodEnd = d.ScheduledChange != nil && d.ScheduledChange.Action == "cancel"
		switch d.Status {
		case StatusPastDue:
			if sub.PastDueSince == nil {
				t := ev.OccurredAt
				sub.PastDueSince = &t
			}
		default:
			sub.PastDueSince = nil
		}

		price := d.PriceOf()
		newPkg, mapped := "", false
		if price != "" {
			newPkg, mapped = prices(price)
			if !mapped {
				out.UnmappedPrice = price
			}
		}
		switch d.Status {
		case StatusActive, StatusTrialing, StatusPastDue:
			if mapped {
				switch {
				case entitlements.Rank(newPkg) < entitlements.Rank(curPackage) && cur.PriceID != "" && cur.PriceID != price &&
					cur.CurrentPeriodEnd != nil && now.Before(*cur.CurrentPeriodEnd) && !periodRolled(cur, sub):
					// Downgrade (§4): Paddle already carries the new
					// item (billed at the next renewal); we keep the
					// current entitlements until the period ends.
					sub.ScheduledPriceID = price
					sub.PriceID = cur.PriceID
				default:
					sub.PriceID = price
					sub.ScheduledPriceID = ""
					if newPkg != curPackage {
						out.PackageCode = newPkg
					}
				}
			} else {
				sub.PriceID = price
			}
			if d.Status == StatusActive && cur.Status != StatusActive {
				out.ClearTrial = true
			}
		case StatusCanceled, StatusPaused:
			sub.PriceID = price
			sub.ScheduledPriceID = ""
			if curPackage != entitlements.PackageWatch {
				out.PackageCode = entitlements.PackageWatch
			}
		}
		out.Sub = sub
		return out, nil

	case "transaction.completed":
		var d TransactionData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return out, fmt.Errorf("%w: transaction data: %v", ErrBadRequest, err)
		}
		sub := cur
		if sub.OrgID == 0 {
			if id, ok := orgIDFrom(d.CustomData); ok {
				sub.OrgID = id
			}
		}
		if d.CustomerID != "" && sub.CustomerID == "" {
			sub.CustomerID = d.CustomerID
		}
		if d.SubscriptionID != "" && sub.SubscriptionID == "" {
			sub.SubscriptionID = d.SubscriptionID
		}
		if sub.Status == "" {
			// A transaction can land before subscription.created; the
			// row exists so the portal works, status follows shortly.
			sub.Status = StatusActive
		}
		if sub.UpdatedAt.IsZero() || ev.OccurredAt.After(sub.UpdatedAt) {
			sub.UpdatedAt = ev.OccurredAt
		}
		out.Sub = sub
		out.AffiliateTxnID = d.ID
		out.Net = d.Details.Totals.Earnings
		return out, nil
	}
	// Unknown types are acknowledged and stored, nothing applied.
	return out, nil
}

// periodRolled reports whether the incoming state starts a new billing
// period relative to the stored one (the renewal that makes a scheduled
// downgrade effective).
func periodRolled(cur, next Subscription) bool {
	return cur.CurrentPeriodEnd != nil && next.CurrentPeriodEnd != nil && next.CurrentPeriodEnd.After(*cur.CurrentPeriodEnd)
}

// ApplyScheduled makes a deferred downgrade effective once the period
// it was deferred to has ended (job path; the renewal webhook normally
// gets there first).
func ApplyScheduled(sub Subscription, prices PriceLookup, now time.Time) (Subscription, string, bool) {
	if sub.ScheduledPriceID == "" || sub.CurrentPeriodEnd == nil || now.Before(*sub.CurrentPeriodEnd) {
		return sub, "", false
	}
	pkg, ok := prices(sub.ScheduledPriceID)
	if !ok {
		return sub, "", false
	}
	sub.PriceID, sub.ScheduledPriceID, sub.UpdatedAt = sub.ScheduledPriceID, "", now
	return sub, pkg, true
}

// ProrationFor picks the Paddle proration_billing_mode for a plan
// change (packages.md §4; paddle:subscription-update): upgrades charge
// the difference now, downgrades switch the item at Paddle now but bill
// the full new price only at the next renewal — no refund, no credit —
// while Apply keeps the old entitlements until the period ends.
func ProrationFor(fromPackage, toPackage string) string {
	if entitlements.Rank(toPackage) > entitlements.Rank(fromPackage) {
		return "prorated_immediately"
	}
	return "full_next_billing_period"
}
