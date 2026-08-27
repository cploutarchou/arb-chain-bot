package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/billing/paddle"
)

// Billing implements paddle.Store over subscriptions / paddle_events /
// billing_prices (migration 000014). Paddle is the source of truth;
// every write here originates from a verified webhook or from the
// operator's price mapping.
type Billing struct{ s *Store }

func (s *Store) Billing() *Billing { return &Billing{s: s} }

var _ paddle.Store = (*Billing)(nil)

func (b *Billing) RecordEvent(ctx context.Context, ev paddle.Event) (bool, error) {
	tag, err := b.s.Pool.Exec(ctx, `
		INSERT INTO paddle_events (event_id, type, occurred_at, payload)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id) DO NOTHING`, ev.EventID, ev.EventType, ev.OccurredAt, ev.Raw)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	// Seen before: fresh only if the earlier delivery never finished.
	var processed *time.Time
	if err := b.s.Pool.QueryRow(ctx, `SELECT processed_at FROM paddle_events WHERE event_id = $1`, ev.EventID).Scan(&processed); err != nil {
		return false, err
	}
	return processed == nil, nil
}

func (b *Billing) MarkProcessed(ctx context.Context, eventID string, at time.Time) error {
	_, err := b.s.Pool.Exec(ctx, `UPDATE paddle_events SET processed_at = $2 WHERE event_id = $1`, eventID, at)
	return err
}

func (b *Billing) Subscription(ctx context.Context, orgID int64) (paddle.Subscription, bool, error) {
	row := b.s.Pool.QueryRow(ctx, `
		SELECT org_id, COALESCE(paddle_customer_id,''), COALESCE(paddle_subscription_id,''), COALESCE(price_id,''),
		       status, current_period_end, cancel_at_period_end, past_due_since, COALESCE(scheduled_price_id,''), updated_at
		FROM subscriptions WHERE org_id = $1`, orgID)
	sub, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return paddle.Subscription{}, false, nil
	}
	return sub, err == nil, err
}

func (b *Billing) SubscriptionByPaddleID(ctx context.Context, subscriptionID string) (paddle.Subscription, bool, error) {
	row := b.s.Pool.QueryRow(ctx, `
		SELECT org_id, COALESCE(paddle_customer_id,''), COALESCE(paddle_subscription_id,''), COALESCE(price_id,''),
		       status, current_period_end, cancel_at_period_end, past_due_since, COALESCE(scheduled_price_id,''), updated_at
		FROM subscriptions WHERE paddle_subscription_id = $1`, subscriptionID)
	sub, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return paddle.Subscription{}, false, nil
	}
	return sub, err == nil, err
}

func scanSubscription(row pgx.Row) (paddle.Subscription, error) {
	var s paddle.Subscription
	err := row.Scan(&s.OrgID, &s.CustomerID, &s.SubscriptionID, &s.PriceID, &s.Status, &s.CurrentPeriodEnd,
		&s.CancelAtPeriodEnd, &s.PastDueSince, &s.ScheduledPriceID, &s.UpdatedAt)
	return s, err
}

func (b *Billing) UpsertSubscription(ctx context.Context, s paddle.Subscription) error {
	_, err := b.s.Pool.Exec(ctx, `
		INSERT INTO subscriptions
			(org_id, paddle_customer_id, paddle_subscription_id, price_id, status, current_period_end,
			 cancel_at_period_end, past_due_since, scheduled_price_id, updated_at)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''), $5, $6, $7, $8, NULLIF($9,''), $10)
		ON CONFLICT (org_id) DO UPDATE SET
			paddle_customer_id = COALESCE(EXCLUDED.paddle_customer_id, subscriptions.paddle_customer_id),
			paddle_subscription_id = COALESCE(EXCLUDED.paddle_subscription_id, subscriptions.paddle_subscription_id),
			price_id = EXCLUDED.price_id, status = EXCLUDED.status,
			current_period_end = EXCLUDED.current_period_end, cancel_at_period_end = EXCLUDED.cancel_at_period_end,
			past_due_since = EXCLUDED.past_due_since, scheduled_price_id = EXCLUDED.scheduled_price_id,
			updated_at = EXCLUDED.updated_at`,
		s.OrgID, s.CustomerID, s.SubscriptionID, s.PriceID, s.Status, s.CurrentPeriodEnd,
		s.CancelAtPeriodEnd, s.PastDueSince, s.ScheduledPriceID, s.UpdatedAt)
	return err
}

func (b *Billing) PackageForPrice(ctx context.Context, priceID string) (string, string, bool, error) {
	var code, interval string
	err := b.s.Pool.QueryRow(ctx, `SELECT package_code, billing_interval FROM billing_prices WHERE price_id = $1`, priceID).Scan(&code, &interval)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	return code, interval, err == nil, err
}

func (b *Billing) SetPrice(ctx context.Context, priceID, packageCode, interval string) error {
	_, err := b.s.Pool.Exec(ctx, `
		INSERT INTO billing_prices (price_id, package_code, billing_interval) VALUES ($1, $2, $3)
		ON CONFLICT (price_id) DO UPDATE SET package_code = EXCLUDED.package_code, billing_interval = EXCLUDED.billing_interval`,
		priceID, packageCode, interval)
	return err
}

func (b *Billing) ListPrices(ctx context.Context) ([]paddle.Price, error) {
	rows, err := b.s.Pool.Query(ctx, `SELECT price_id, package_code, billing_interval FROM billing_prices ORDER BY package_code, billing_interval`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []paddle.Price{}
	for rows.Next() {
		var p paddle.Price
		if err := rows.Scan(&p.PriceID, &p.PackageCode, &p.Interval); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
