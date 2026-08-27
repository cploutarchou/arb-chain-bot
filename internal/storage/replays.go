package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/jobrun"
	"github.com/cploutarchou/arb-chain-bot/internal/replay"
)

// UpsertReplayRun writes the run's current state (insert or replace).
// Unlike campaign_runs, replay_runs has no serialized "request" blob:
// replay.Request is exactly {recording, config_version, speed}, which
// the table already stores as plain columns (recording_id/config_version/
// speed) — Get/List reconstruct Request from those, not from JSON.
func (s *Store) UpsertReplayRun(ctx context.Context, run replay.Run) error {
	var top []byte
	if run.Top != nil {
		var err error
		if top, err = json.Marshal(run.Top); err != nil {
			return err
		}
	}
	var cfgVersion *int64
	if run.Request.ConfigVersion != 0 {
		v := run.Request.ConfigVersion
		cfgVersion = &v
	}
	var speed *float64
	if run.Request.Speed != 0 {
		v := run.Request.Speed
		speed = &v
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO replay_runs
			(id, recording_id, config_version, speed, status, done, total, step, created_at,
			 started_at, finished_at, error, opportunities, qualified, cycles, top, actor,
			 owner_id, heartbeat_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8,''), $9, $10, $11, NULLIF($12,''),
			$13, $14, $15, $16, NULLIF($17,''), NULLIF($18,''), $19)
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status, done = EXCLUDED.done, total = EXCLUDED.total,
			step = EXCLUDED.step, started_at = EXCLUDED.started_at,
			finished_at = EXCLUDED.finished_at, error = EXCLUDED.error,
			opportunities = EXCLUDED.opportunities, qualified = EXCLUDED.qualified,
			cycles = EXCLUDED.cycles, top = EXCLUDED.top,
			owner_id = EXCLUDED.owner_id, heartbeat_at = EXCLUDED.heartbeat_at`,
		run.ID, run.Recording, cfgVersion, speed, run.Status, run.Done, run.Total, run.Step, run.CreatedAt,
		run.StartedAt, run.FinishedAt, run.Error, run.Evaluations, run.Qualified, run.Cycles, top, run.Actor,
		run.OwnerID, nullTime(run.HeartbeatAt))
	return err
}

// speed is cast to text (not scanned as NUMERIC directly), matching the
// project-wide convention for money/rate columns (see orders.latency_ms
// / fee columns in orders_fills.go) rather than relying on pgx's numeric
// codec to satisfy a raw float64 target.
const replayRunColumns = `id, recording_id, COALESCE(config_version,0), COALESCE(speed::text,''),
	status, done, total, COALESCE(step,''), created_at, started_at, finished_at,
	COALESCE(error,''), COALESCE(opportunities,0), COALESCE(qualified,0), COALESCE(cycles,0),
	top, COALESCE(actor,''), COALESCE(owner_id,''), heartbeat_at`

func scanReplayRun(row pgx.Row) (replay.Run, error) {
	var (
		r         replay.Run
		speed     string
		top       []byte
		heartbeat *time.Time
	)
	if err := row.Scan(&r.ID, &r.Recording, &r.Request.ConfigVersion, &speed,
		&r.Status, &r.Done, &r.Total, &r.Step, &r.CreatedAt, &r.StartedAt, &r.FinishedAt,
		&r.Error, &r.Evaluations, &r.Qualified, &r.Cycles, &top, &r.Actor,
		&r.OwnerID, &heartbeat); err != nil {
		return r, err
	}
	if heartbeat != nil {
		r.HeartbeatAt = *heartbeat
	}
	r.Request.Recording = r.Recording
	if speed != "" {
		v, err := strconv.ParseFloat(speed, 64)
		if err != nil {
			return r, fmt.Errorf("storage: replay run %s speed: %w", r.ID, err)
		}
		r.Request.Speed = v
	}
	if len(top) > 0 {
		if err := json.Unmarshal(top, &r.Top); err != nil {
			return r, fmt.Errorf("storage: replay run %s top: %w", r.ID, err)
		}
	}
	return r, nil
}

// ListReplayRuns returns runs newest-first.
func (s *Store) ListReplayRuns(ctx context.Context, limit int) ([]replay.Run, error) {
	// review P3(h): clamp INTO [1, RingCap] — the old
	// `if limit <= 0 || limit > 200 { limit = 50 }` shape silently
	// bounced anything OVER the cap down to the default instead of
	// clamping it TO the cap.
	limit = jobrun.ClampLimit(limit, 50, jobrun.RingCap)
	rows, err := s.Pool.Query(ctx, `SELECT `+replayRunColumns+`
		FROM replay_runs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []replay.Run
	for rows.Next() {
		r, err := scanReplayRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetReplayRun returns one run.
func (s *Store) GetReplayRun(ctx context.Context, id string) (replay.Run, error) {
	r, err := scanReplayRun(s.Pool.QueryRow(ctx, `SELECT `+replayRunColumns+`
		FROM replay_runs WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return replay.Run{}, replay.ErrNotFound
	}
	return r, err
}
