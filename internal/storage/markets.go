package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// UpsertMarkets syncs normalized instrument metadata after each metadata
// refresh; rules land as JSONB for the console's market explorer.
func (s *Store) UpsertMarkets(ctx context.Context, markets []exchange.Market) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	seen := map[exchange.ExchangeID]bool{}
	for _, m := range markets {
		if !seen[m.ID.Exchange] {
			if _, err := tx.Exec(ctx, `
				INSERT INTO exchanges (id, name) VALUES ($1, $1)
				ON CONFLICT DO NOTHING`, string(m.ID.Exchange)); err != nil {
				return err
			}
			seen[m.ID.Exchange] = true
		}
		rules, err := json.Marshal(m.Rules)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO markets (id, exchange_id, symbol, base_asset, quote_asset, enabled, status, rules, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
			ON CONFLICT (id) DO UPDATE SET
				base_asset = EXCLUDED.base_asset,
				quote_asset = EXCLUDED.quote_asset,
				enabled = EXCLUDED.enabled,
				status = EXCLUDED.status,
				rules = EXCLUDED.rules,
				updated_at = now()`,
			m.ID.String(), string(m.ID.Exchange), string(m.ID.Symbol),
			string(m.Base), string(m.Quote), m.Enabled, string(m.Status), rules); err != nil {
			return fmt.Errorf("storage: market %s: %w", m.ID, err)
		}
	}
	return tx.Commit(ctx)
}
