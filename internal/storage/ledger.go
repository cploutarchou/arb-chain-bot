package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LedgerBalance is one asset's cash position in the reservation ledger.
type LedgerBalance struct {
	Available string `json:"available"`
	Reserved  string `json:"reserved"`
}

// LedgerSnapshot is the paper session's complete accounting state at one
// instant: the reservation ledger's cash per start asset and the
// portfolio's economics. One is written after every settlement so a
// restart continues the session instead of resetting its loss, drawdown,
// exposure and fees (the tables it fills were part of the schema from the
// start and had no writer). All amounts are decimal strings.
type LedgerSnapshot struct {
	SessionID  string
	ExchangeID string
	At         time.Time

	Balances   map[string]LedgerBalance // start asset → cash
	MarkValues map[string]string        // start asset → marked value of stranded exposure
	Realized   map[string]string        // start asset → realized PnL
	Peak       map[string]string        // start asset → equity high-water mark
	Drawdown   map[string]string        // start asset → worst peak-to-trough fraction
	Fees       map[string]string        // fee asset → cumulative fees
	Exposure   map[string]string        // asset → stranded quantity

	Cycles, Completed, Failed int64
}

// pnlDimension is the JSON detail stored beside each per-asset PnL row.
type pnlDimension struct {
	Asset     string            `json:"asset"`
	Peak      string            `json:"peak,omitempty"`
	Fees      map[string]string `json:"fees,omitempty"`
	Exposure  map[string]string `json:"exposure,omitempty"`
	Cycles    int64             `json:"cycles"`
	Completed int64             `json:"completed"`
	Failed    int64             `json:"failed"`
}

// InsertLedgerSnapshot persists one snapshot in a single transaction:
// virtual_balances is upserted (the current cash per asset),
// balance_snapshots and pnl_snapshots receive one row (per start asset
// for the latter) stamped with the same timestamp so the reader can
// join them back into one state.
func (s *Store) InsertLedgerSnapshot(ctx context.Context, snap *LedgerSnapshot) error {
	if snap == nil || snap.SessionID == "" {
		return errors.New("storage: ledger snapshot without a session")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if snap.ExchangeID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO exchanges (id, name) VALUES ($1, $1) ON CONFLICT DO NOTHING`,
			snap.ExchangeID); err != nil {
			return fmt.Errorf("storage: exchange ref: %w", err)
		}
		for asset, b := range snap.Balances {
			if _, err := tx.Exec(ctx, `
				INSERT INTO virtual_balances (session_id, exchange_id, asset, available, reserved, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (session_id, exchange_id, asset)
				DO UPDATE SET available = EXCLUDED.available, reserved = EXCLUDED.reserved, updated_at = EXCLUDED.updated_at`,
				snap.SessionID, snap.ExchangeID, asset, b.Available, b.Reserved, snap.At); err != nil {
				return fmt.Errorf("storage: virtual balance %s: %w", asset, err)
			}
		}
	}
	balances, err := json.Marshal(snap.Balances)
	if err != nil {
		return err
	}
	marks, err := json.Marshal(snap.MarkValues)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO balance_snapshots (session_id, ts, balances, mark_values) VALUES ($1, $2, $3, $4)`,
		snap.SessionID, snap.At, balances, marks); err != nil {
		return fmt.Errorf("storage: balance snapshot: %w", err)
	}
	for asset := range snap.Balances {
		dim, err := json.Marshal(pnlDimension{
			Asset: asset, Peak: snap.Peak[asset], Fees: snap.Fees, Exposure: snap.Exposure,
			Cycles: snap.Cycles, Completed: snap.Completed, Failed: snap.Failed,
		})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO pnl_snapshots (session_id, ts, realized, unrealized, fees_paid, drawdown, by_dimension)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			snap.SessionID, snap.At,
			numericOr(snap.Realized[asset], "0"), numericOr(snap.MarkValues[asset], "0"),
			numericOr(snap.Fees[asset], "0"), nullStr(snap.Drawdown[asset]), dim); err != nil {
			return fmt.Errorf("storage: pnl snapshot %s: %w", asset, err)
		}
	}
	return tx.Commit(ctx)
}

func numericOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// LatestLedgerSnapshot reads the most recent snapshot of one session;
// ok is false when the session has none.
func (s *Store) LatestLedgerSnapshot(ctx context.Context, sessionID string) (LedgerSnapshot, bool, error) {
	var snap LedgerSnapshot
	var balances, marks []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT ts, balances, mark_values FROM balance_snapshots
		WHERE session_id = $1 ORDER BY ts DESC, id DESC LIMIT 1`, sessionID).Scan(&snap.At, &balances, &marks)
	if errors.Is(err, pgx.ErrNoRows) {
		return LedgerSnapshot{}, false, nil
	}
	if err != nil {
		return LedgerSnapshot{}, false, err
	}
	snap.SessionID = sessionID
	if err := json.Unmarshal(balances, &snap.Balances); err != nil {
		return LedgerSnapshot{}, false, fmt.Errorf("storage: balance snapshot: %w", err)
	}
	if len(marks) > 0 {
		if err := json.Unmarshal(marks, &snap.MarkValues); err != nil {
			return LedgerSnapshot{}, false, fmt.Errorf("storage: mark values: %w", err)
		}
	}
	snap.Realized = map[string]string{}
	snap.Peak = map[string]string{}
	snap.Drawdown = map[string]string{}
	rows, err := s.Pool.Query(ctx, `
		SELECT realized, drawdown, by_dimension FROM pnl_snapshots
		WHERE session_id = $1 AND ts = $2 ORDER BY id`, sessionID, snap.At)
	if err != nil {
		return LedgerSnapshot{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var realized string
		var drawdown *string
		var dimRaw []byte
		if err := rows.Scan(&realized, &drawdown, &dimRaw); err != nil {
			return LedgerSnapshot{}, false, err
		}
		var dim pnlDimension
		if err := json.Unmarshal(dimRaw, &dim); err != nil {
			return LedgerSnapshot{}, false, fmt.Errorf("storage: pnl dimension: %w", err)
		}
		snap.Realized[dim.Asset] = realized
		if drawdown != nil {
			snap.Drawdown[dim.Asset] = *drawdown
		}
		if dim.Peak != "" {
			snap.Peak[dim.Asset] = dim.Peak
		}
		// Fees, exposure and counters are session-wide; every row carries
		// the same copy.
		snap.Fees, snap.Exposure = dim.Fees, dim.Exposure
		snap.Cycles, snap.Completed, snap.Failed = dim.Cycles, dim.Completed, dim.Failed
	}
	if err := rows.Err(); err != nil {
		return LedgerSnapshot{}, false, err
	}
	if snap.ExchangeID == "" {
		_ = s.Pool.QueryRow(ctx, `SELECT exchange_id FROM virtual_balances WHERE session_id = $1 LIMIT 1`,
			sessionID).Scan(&snap.ExchangeID)
	}
	return snap, true, nil
}

// OpenPaperSession is the most recent paper session that has not been
// ended, with the starting balances it was registered with.
type OpenPaperSession struct {
	ID               string
	Mode             string
	StartedAt        time.Time
	StartingBalances map[string]string
}

// LatestOpenPaperSession finds the session a restart may resume; ok is
// false when every session has been ended (or none exists).
func (s *Store) LatestOpenPaperSession(ctx context.Context, mode string) (OpenPaperSession, bool, error) {
	var out OpenPaperSession
	var balances []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT id, mode, started_at, starting_balances FROM paper_sessions
		WHERE mode = $1 AND ended_at IS NULL ORDER BY started_at DESC LIMIT 1`, mode).
		Scan(&out.ID, &out.Mode, &out.StartedAt, &balances)
	if errors.Is(err, pgx.ErrNoRows) {
		return OpenPaperSession{}, false, nil
	}
	if err != nil {
		return OpenPaperSession{}, false, err
	}
	if err := json.Unmarshal(balances, &out.StartingBalances); err != nil {
		return OpenPaperSession{}, false, fmt.Errorf("storage: starting balances: %w", err)
	}
	return out, true, nil
}
