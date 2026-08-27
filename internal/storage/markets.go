package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
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

// ListMarkets returns the persisted market metadata for one exchange —
// the API-profile fallback Catalog (platform.Catalog) when no live
// Engine is attached (design §1.5); the engine profile prefers its own
// retained bootstrap slice instead of round-tripping through storage.
func (s *Store) ListMarkets(ctx context.Context, ex exchange.ExchangeID) ([]exchange.Market, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT symbol, base_asset, quote_asset, status, enabled, rules
		FROM markets WHERE exchange_id = $1`, string(ex))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exchange.Market
	for rows.Next() {
		var (
			m     exchange.Market
			rules []byte
		)
		if err := rows.Scan(&m.ID.Symbol, &m.Base, &m.Quote, &m.Status, &m.Enabled, &rules); err != nil {
			return nil, err
		}
		m.ID.Exchange = ex
		if len(rules) > 0 {
			if err := json.Unmarshal(rules, &m.Rules); err != nil {
				return nil, err
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Catalog adapts the store to platform.Catalog — the API-profile
// fallback when no *app.Engine is attached (design §1.5). An empty
// result is reported as platform.ErrCatalogNotReady, the same "not
// bootstrapped yet, not genuinely empty" distinction engineCatalog
// makes, so the two implementations behave identically from the
// platform.Service caller's point of view.
type Catalog struct{ S *Store }

func (c Catalog) Markets(ctx context.Context, ex exchange.ExchangeID) ([]exchange.Market, error) {
	out, err := c.S.ListMarkets(ctx, ex)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, platform.ErrCatalogNotReady
	}
	return out, nil
}
