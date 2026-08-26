package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
)

// UpsertRecordingSegment registers a closed segment under its recording
// session row (market_recording_metadata): first segment inserts the row,
// later ones append to segment_files and advance ended_at.
func (s *Store) UpsertRecordingSegment(ctx context.Context, recordingID string, exchangeID exchange.ExchangeID, streams map[uint16]exchange.Symbol, startedAt time.Time, meta marketdata.SegmentMeta) error {
	streamDoc := make(map[string]string, len(streams))
	for id, sym := range streams {
		streamDoc[string(sym)] = string(rune('0' + id)) // small ids; readable
	}
	streamsJSON, err := json.Marshal(streamDoc)
	if err != nil {
		return err
	}
	segJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO exchanges (id, name) VALUES ($1, $1) ON CONFLICT DO NOTHING`,
		string(exchangeID)); err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO market_recording_metadata
			(id, exchange_id, started_at, ended_at, streams, segment_files)
		VALUES ($1, $2, $3, $4, $5, jsonb_build_array($6::jsonb))
		ON CONFLICT (id) DO UPDATE SET
			segment_files = market_recording_metadata.segment_files || $6::jsonb,
			ended_at = EXCLUDED.ended_at`,
		recordingID, string(exchangeID), startedAt, meta.ToTS, streamsJSON, segJSON)
	return err
}
