package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/campaign"
)

// UpsertCampaignRun writes the run's current state (insert or replace).
func (s *Store) UpsertCampaignRun(ctx context.Context, run campaign.Run) error {
	req, err := json.Marshal(run.Request)
	if err != nil {
		return err
	}
	var flags []byte
	if run.Flags != nil {
		if flags, err = json.Marshal(run.Flags); err != nil {
			return err
		}
	}
	var verdicts []byte
	if run.Verdicts != nil {
		if verdicts, err = json.Marshal(run.Verdicts); err != nil {
			return err
		}
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO campaign_runs
			(id, recording_id, request, status, done, total, step, created_at,
			 started_at, finished_at, error, flags, verdicts, report_md, report_path, json_path, actor)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,''), $8, $9, $10, NULLIF($11,''),
			$12, $13, NULLIF($14,''), NULLIF($15,''), NULLIF($16,''), NULLIF($17,''))
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status, done = EXCLUDED.done, total = EXCLUDED.total,
			step = EXCLUDED.step, started_at = EXCLUDED.started_at,
			finished_at = EXCLUDED.finished_at, error = EXCLUDED.error,
			flags = EXCLUDED.flags, verdicts = EXCLUDED.verdicts, report_md = EXCLUDED.report_md,
			report_path = EXCLUDED.report_path, json_path = EXCLUDED.json_path`,
		run.ID, run.Recording, req, run.Status, run.Done, run.Total, run.Step, run.CreatedAt,
		run.StartedAt, run.FinishedAt, run.Error, flags, verdicts, run.ReportMD, run.ReportPath, run.JSONPath, run.Actor)
	return err
}

const campaignRunColumns = `id, recording_id, request, status, done, total, COALESCE(step,''),
	created_at, started_at, finished_at, COALESCE(error,''), flags, verdicts,
	COALESCE(report_md,''), COALESCE(report_path,''), COALESCE(json_path,''), COALESCE(actor,'')`

func scanCampaignRun(row pgx.Row, withReport bool) (campaign.Run, error) {
	var (
		r        campaign.Run
		req      []byte
		flags    []byte
		verdicts []byte
	)
	if err := row.Scan(&r.ID, &r.Recording, &req, &r.Status, &r.Done, &r.Total, &r.Step,
		&r.CreatedAt, &r.StartedAt, &r.FinishedAt, &r.Error, &flags, &verdicts,
		&r.ReportMD, &r.ReportPath, &r.JSONPath, &r.Actor); err != nil {
		return r, err
	}
	if err := json.Unmarshal(req, &r.Request); err != nil {
		return r, fmt.Errorf("storage: campaign run %s request: %w", r.ID, err)
	}
	if len(flags) > 0 {
		if err := json.Unmarshal(flags, &r.Flags); err != nil {
			return r, fmt.Errorf("storage: campaign run %s flags: %w", r.ID, err)
		}
	}
	if len(verdicts) > 0 {
		if err := json.Unmarshal(verdicts, &r.Verdicts); err != nil {
			return r, fmt.Errorf("storage: campaign run %s verdicts: %w", r.ID, err)
		}
	}
	if !withReport {
		r.ReportMD = ""
	}
	return r, nil
}

// ListCampaignRuns returns runs newest-first without report bodies.
func (s *Store) ListCampaignRuns(ctx context.Context, limit int) ([]campaign.Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+campaignRunColumns+`
		FROM campaign_runs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []campaign.Run
	for rows.Next() {
		r, err := scanCampaignRun(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetCampaignRun returns one run with its report.
func (s *Store) GetCampaignRun(ctx context.Context, id string) (campaign.Run, error) {
	r, err := scanCampaignRun(s.Pool.QueryRow(ctx, `SELECT `+campaignRunColumns+`
		FROM campaign_runs WHERE id = $1`, id), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.Run{}, campaign.ErrNotFound
	}
	return r, err
}
