package storage

import (
	"context"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
)

// Alerts adapts the store to notification.CenterStore. acked_by /
// resolved_by carry FK'd platform user IDs; actors without a users row
// (Telegram identities) persist as NULL here — the audit trail records
// the concrete actor.
type Alerts struct{ s *Store }

func (s *Store) Alerts() *Alerts { return &Alerts{s: s} }

func (a *Alerts) UpsertAlert(ctx context.Context, al notification.Alert) error {
	_, err := a.s.Pool.Exec(ctx, `
		INSERT INTO alerts (id, ts, severity, source, title, body, dedup_key, state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO NOTHING`,
		al.ID, al.FirstAt, al.SevName, al.Source, al.Title, al.Body, al.Key, string(al.State))
	return err
}

func (a *Alerts) UpdateAlertState(ctx context.Context, id string, state notification.AlertState, actor string, at time.Time) error {
	var col, byCol string
	switch state {
	case notification.AlertAcked:
		col, byCol = "acked_at", "acked_by"
	case notification.AlertResolved:
		col, byCol = "resolved_at", "resolved_by"
	default:
		return nil
	}
	// Only a real users.id satisfies the FK; other actors persist NULL.
	_, err := a.s.Pool.Exec(ctx, `
		UPDATE alerts SET state = $2, `+col+` = $3,
			`+byCol+` = (SELECT id FROM users WHERE id = $4)
		WHERE id = $1`,
		id, string(state), at, actor)
	return err
}
