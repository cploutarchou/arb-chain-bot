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

// LoadActive returns persisted unresolved alerts (oldest first) so the
// center can rehydrate after a restart (audit CR-P2-10). Count and
// LastAt are not persisted per delivery; they hydrate as 1 / first-seen.
func (a *Alerts) LoadActive(ctx context.Context, limit int) ([]notification.Alert, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := a.s.Pool.Query(ctx, `
		SELECT id, ts, severity, source, title, body, dedup_key, state,
		       COALESCE(acked_by, ''), COALESCE(acked_at, 'epoch'::timestamptz)
		FROM alerts WHERE state <> 'resolved'
		ORDER BY ts ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sevOf := map[string]notification.Severity{
		"INFO":     notification.SeverityInfo,
		"WARNING":  notification.SeverityWarning,
		"CRITICAL": notification.SeverityCritical,
	}
	var out []notification.Alert
	for rows.Next() {
		var al notification.Alert
		var state string
		var ackedAt time.Time
		if err := rows.Scan(&al.ID, &al.FirstAt, &al.SevName, &al.Source, &al.Title,
			&al.Body, &al.Key, &state, &al.AckedBy, &ackedAt); err != nil {
			return nil, err
		}
		al.Severity = sevOf[al.SevName]
		al.State = notification.AlertState(state)
		al.LastAt = al.FirstAt
		al.Count = 1
		if ackedAt.Unix() > 0 {
			al.AckedAt = ackedAt
		}
		out = append(out, al)
	}
	return out, rows.Err()
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
