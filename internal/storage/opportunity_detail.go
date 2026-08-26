package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrOpportunityNotFound reports an unknown opportunity id (GetOpportunity).
var ErrOpportunityNotFound = errors.New("storage: opportunity not found")

// OpportunityDecisionView is the persisted risk verdict (BL-27).
type OpportunityDecisionView struct {
	Allowed       bool            `json:"allowed"`
	ReasonCode    string          `json:"reason_code,omitempty"`
	Checks        json.RawMessage `json:"checks,omitempty"`
	ConfigVersion int64           `json:"config_version,omitempty"`
	// Legacy is true when this decision was reconstructed from the older
	// `legs.risk_checks` shape (rows persisted before migration 000007
	// added the `decision` column) rather than read directly — Checks is
	// present either way, but Allowed/ConfigVersion are best-effort on
	// legacy rows (config_version falls back to the opportunity's own
	// config_version; allowed is inferred from reason_code being empty).
	Legacy bool `json:"legacy,omitempty"`
}

// SimulationResultView is the settled paper cycle for this opportunity,
// when one was recorded (BL-27's "simulation result when recorded" — no
// new column needed, paper_cycles is already keyed by opportunity_id).
type SimulationResultView struct {
	CycleID     string          `json:"cycle_id"`
	Outcome     string          `json:"outcome"`
	PnLAmount   *string         `json:"pnl_amount,omitempty"`
	PnLAsset    *string         `json:"pnl_asset,omitempty"`
	Fees        json.RawMessage `json:"fees,omitempty"`
	SlippageBps *string         `json:"slippage_bps,omitempty"`
	Exposure    json.RawMessage `json:"exposure,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	SettledAt   *time.Time      `json:"settled_at,omitempty"`
}

// OpportunityDetail is GET /api/v1/opportunities/{id}'s payload (BL-27):
// the full economics row plus the persisted decision/reason, the book
// versions the quote was computed from, and the simulation result when
// one exists.
type OpportunityDetail struct {
	ID             string          `json:"id"`
	ExchangeID     string          `json:"exchange_id"`
	TriangleID     string          `json:"triangle_id"`
	Status         string          `json:"status"`
	ReasonCode     *string         `json:"reason_code,omitempty"`
	StartAsset     string          `json:"starting_asset"`
	StartAmount    string          `json:"starting_amount"`
	Legs           json.RawMessage `json:"legs"`
	GrossFinal     *string         `json:"gross_final_amount,omitempty"`
	EstimatedFinal *string         `json:"estimated_final_amount,omitempty"`
	GrossProfit    *string         `json:"gross_profit,omitempty"`
	NetProfit      *string         `json:"net_profit,omitempty"`
	GrossReturnBps *string         `json:"gross_return_bps,omitempty"`
	NetReturnBps   *string         `json:"net_return_bps,omitempty"`
	DataQuality    *string         `json:"data_quality,omitempty"`
	ConfigVersion  *int64          `json:"config_version,omitempty"`
	DetectedAt     time.Time       `json:"detected_at"`
	ExpiresAt      *time.Time      `json:"expires_at,omitempty"`
	DecidedAt      *time.Time      `json:"decided_at,omitempty"`

	Decision     *OpportunityDecisionView `json:"decision,omitempty"`
	BookVersions []int64                  `json:"book_versions,omitempty"`
	Simulation   *SimulationResultView    `json:"simulation,omitempty"`
	Notes        []string                 `json:"notes,omitempty"`
}

// legacyLegsShape mirrors the {legs, risk_checks} object InsertOpportunity
// has always folded into the `legs` column — the fallback source for
// Decision.Checks on rows persisted before the `decision` column existed.
type legacyLegsShape struct {
	Checks json.RawMessage `json:"risk_checks,omitempty"`
}

// GetOpportunity serves GET /api/v1/opportunities/{id}.
func (s *Store) GetOpportunity(ctx context.Context, id string) (OpportunityDetail, error) {
	var (
		d            OpportunityDetail
		decisionJSON []byte
		bookVersions []int64
	)
	err := s.Pool.QueryRow(ctx, `
		SELECT id, exchange_id, triangle_id, status, reason_code,
		       starting_asset, starting_amount::text, legs,
		       gross_final_amount::text, estimated_final_amount::text,
		       gross_profit::text, net_profit::text, gross_return_bps::text, net_return_bps::text,
		       data_quality::text, config_version, detected_at, expires_at, decided_at,
		       decision, book_versions
		FROM opportunities WHERE id = $1`, id).Scan(
		&d.ID, &d.ExchangeID, &d.TriangleID, &d.Status, &d.ReasonCode,
		&d.StartAsset, &d.StartAmount, &d.Legs,
		&d.GrossFinal, &d.EstimatedFinal,
		&d.GrossProfit, &d.NetProfit, &d.GrossReturnBps, &d.NetReturnBps,
		&d.DataQuality, &d.ConfigVersion, &d.DetectedAt, &d.ExpiresAt, &d.DecidedAt,
		&decisionJSON, &bookVersions,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OpportunityDetail{}, ErrOpportunityNotFound
		}
		return OpportunityDetail{}, fmt.Errorf("storage: get opportunity %s: %w", id, err)
	}
	d.BookVersions = bookVersions

	switch {
	case len(decisionJSON) > 0:
		var dv OpportunityDecisionView
		if err := json.Unmarshal(decisionJSON, &dv); err != nil {
			return OpportunityDetail{}, err
		}
		d.Decision = &dv
	default:
		// Legacy row (persisted before migration 000007): reconstruct
		// what we can from `legs.risk_checks` rather than reporting an
		// absence that isn't true.
		var legacy legacyLegsShape
		if err := json.Unmarshal(d.Legs, &legacy); err == nil && len(legacy.Checks) > 0 {
			dv := OpportunityDecisionView{Checks: legacy.Checks, Legacy: true}
			if d.ReasonCode != nil {
				dv.ReasonCode = *d.ReasonCode
			}
			dv.Allowed = dv.ReasonCode == ""
			if d.ConfigVersion != nil {
				dv.ConfigVersion = *d.ConfigVersion
			}
			d.Decision = &dv
			d.Notes = append(d.Notes, "decision reconstructed from the legacy legs.risk_checks shape (persisted before the decision column existed); allowed/config_version are best-effort")
		}
	}
	if len(d.BookVersions) == 0 {
		d.Notes = append(d.Notes, "book_versions not recorded for this opportunity (persisted before migration 000007, or the quote carried no leg versions)")
	}

	var sim SimulationResultView
	var settledAt *time.Time
	err = s.Pool.QueryRow(ctx, `
		SELECT id, outcome, pnl_amount::text, pnl_asset, fees, slippage_bps::text, exposure, started_at, settled_at
		FROM paper_cycles WHERE opportunity_id = $1
		ORDER BY started_at DESC LIMIT 1`, id).Scan(
		&sim.CycleID, &sim.Outcome, &sim.PnLAmount, &sim.PnLAsset, &sim.Fees, &sim.SlippageBps, &sim.Exposure,
		&sim.StartedAt, &settledAt,
	)
	switch {
	case err == nil:
		sim.SettledAt = settledAt
		d.Simulation = &sim
	case errors.Is(err, pgx.ErrNoRows):
		// No simulation recorded — honest absence, not an error.
	default:
		return OpportunityDetail{}, fmt.Errorf("storage: get opportunity %s simulation: %w", id, err)
	}
	return d, nil
}
