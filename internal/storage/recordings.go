package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
)

// RecordingRow is the list view of one recording session.
type RecordingRow struct {
	ID        string          `json:"id"`
	Exchange  string          `json:"exchange_id"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
	Streams   json.RawMessage `json:"streams"`
	Segments  json.RawMessage `json:"segment_files"`
}

// ListRecordings returns recording sessions newest-first.
func (s *Store) ListRecordings(ctx context.Context, limit int) ([]RecordingRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, exchange_id, started_at, ended_at, streams, segment_files
		FROM market_recording_metadata
		ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecordingRow
	for rows.Next() {
		var r RecordingRow
		if err := rows.Scan(&r.ID, &r.Exchange, &r.StartedAt, &r.EndedAt, &r.Streams, &r.Segments); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordingStreams loads one recording's stream table (stream id →
// symbol) for replay/backtest, inverting the persisted symbol→id map.
func (s *Store) RecordingStreams(ctx context.Context, recordingID string) (map[uint16]exchange.Symbol, error) {
	var doc json.RawMessage
	err := s.Pool.QueryRow(ctx, `
		SELECT streams FROM market_recording_metadata WHERE id = $1`, recordingID).Scan(&doc)
	if err != nil {
		return nil, fmt.Errorf("storage: recording %s: %w", recordingID, err)
	}
	var symToID map[string]string
	if err := json.Unmarshal(doc, &symToID); err != nil {
		return nil, fmt.Errorf("storage: recording %s streams: %w", recordingID, err)
	}
	out := make(map[uint16]exchange.Symbol, len(symToID))
	for sym, idStr := range symToID {
		id, err := strconv.ParseUint(idStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("storage: recording %s stream id %q: %w", recordingID, idStr, err)
		}
		out[uint16(id)] = exchange.Symbol(sym)
	}
	return out, nil
}

// LoadMarkets returns persisted markets with their instrument rules —
// the metadata a backtest needs without exchange egress.
func (s *Store) LoadMarkets(ctx context.Context, exchangeID exchange.ExchangeID) ([]exchange.Market, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, symbol, base_asset, quote_asset, enabled, status, rules
		FROM markets WHERE exchange_id = $1 ORDER BY id`, string(exchangeID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exchange.Market
	for rows.Next() {
		var id, symbol, base, quote, status string
		var enabled bool
		var rulesDoc json.RawMessage
		if err := rows.Scan(&id, &symbol, &base, &quote, &enabled, &status, &rulesDoc); err != nil {
			return nil, err
		}
		m := exchange.Market{
			ID:      exchange.MarketID{Exchange: exchangeID, Symbol: exchange.Symbol(symbol)},
			Base:    exchange.Asset(base),
			Quote:   exchange.Asset(quote),
			Enabled: enabled,
			Status:  exchange.MarketStatus(status),
		}
		if len(rulesDoc) > 0 {
			if err := json.Unmarshal(rulesDoc, &m.Rules); err != nil {
				return nil, fmt.Errorf("storage: market %s rules: %w", id, err)
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpsertRecordingSegment registers a closed segment under its recording
// session row (market_recording_metadata): first segment inserts the row,
// later ones append to segment_files and advance ended_at.
func (s *Store) UpsertRecordingSegment(ctx context.Context, recordingID string, exchangeID exchange.ExchangeID, streams map[uint16]exchange.Symbol, startedAt time.Time, meta marketdata.SegmentMeta) error {
	streamDoc := make(map[string]string, len(streams))
	for id, sym := range streams {
		// strconv, not rune arithmetic: ids >= 10 would render as ':',
		// ';', … and corrupt the stream table (audit CR-P2-9).
		streamDoc[string(sym)] = strconv.FormatUint(uint64(id), 10)
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
