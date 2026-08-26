// Package opportunity models detected triangular opportunities: lifecycle
// states, buffered net economics, TTL discipline, and the revalidation
// contract (SKILL.md §19–§20). An opportunity is immutable evidence plus a
// guarded status; the numbers are never recomputed here — pricing owns
// math, risk owns judgment.
package opportunity

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
)

// Status is the opportunity lifecycle state (docs/architecture.md §16).
type Status string

const (
	StatusDetected    Status = "DETECTED"
	StatusCalculating Status = "CALCULATING"
	StatusQualified   Status = "QUALIFIED"
	StatusRejected    Status = "REJECTED"
	StatusExpired     Status = "EXPIRED"
	StatusReserved    Status = "RESERVED"
	StatusSimulating  Status = "SIMULATING"
	StatusCompleted   Status = "COMPLETED"
	StatusFailed      Status = "FAILED"
)

// legalTransitions is the authoritative transition table.
var legalTransitions = map[Status][]Status{
	StatusDetected:    {StatusCalculating},
	StatusCalculating: {StatusQualified, StatusRejected},
	StatusQualified:   {StatusReserved, StatusExpired},
	StatusReserved:    {StatusSimulating, StatusExpired},
	StatusSimulating:  {StatusCompleted, StatusFailed},
	// REJECTED, EXPIRED, COMPLETED, FAILED are terminal.
}

var (
	ErrIllegalTransition = errors.New("opportunity: illegal status transition")
	ErrNotExpirable      = errors.New("opportunity: status cannot expire")
)

// Buffers are the uncertainty allowances subtracted before qualification,
// in basis points of the deployed input (owned by risk config).
type Buffers struct {
	LatencyBps decimal.Decimal
	RiskBps    decimal.Decimal
}

var tenK = decimal.NewFromInt(10_000)

// Opportunity is one sized, evidenced triangular opportunity.
type Opportunity struct {
	ID         string
	Exchange   exchange.ExchangeID
	TriangleID string
	Start      exchange.Asset

	Quote pricing.CycleQuote // exact executable economics at the chosen size

	Buffers        Buffers
	BufferAmount   decimal.Decimal // start-asset value of the buffers
	EstimatedFinal decimal.Decimal // Quote.FinalAmount - BufferAmount
	NetProfit      decimal.Decimal // EstimatedFinal - InputConsumed
	NetReturnBps   decimal.Decimal
	GrossReturnBps decimal.Decimal // Quote.ReturnBps (fees in, buffers out)

	MaxProfitableSize decimal.Decimal
	RecommendedSize   decimal.Decimal

	DataQuality   decimal.Decimal // 0..1 (book health/age derived, risk input)
	ConfigVersion int64

	DetectedAt time.Time
	ExpiresAt  time.Time

	Status Status
	Reason string // reason code for REJECTED/EXPIRED/FAILED
}

// Build assembles an opportunity from a sized cycle quote. TTL and buffers
// come from versioned config; now comes from the platform Clock
// (deterministic in replay).
func Build(id string, exch exchange.ExchangeID, quote pricing.CycleQuote, b Buffers, ttl time.Duration, now time.Time, configVersion int64) Opportunity {
	input := quote.InputConsumed
	bufferAmt := input.Mul(b.LatencyBps.Add(b.RiskBps)).Div(tenK)
	estFinal := quote.FinalAmount.Sub(bufferAmt)
	netProfit := estFinal.Sub(input)
	var netBps decimal.Decimal
	if input.IsPositive() {
		netBps = estFinal.Div(input).Sub(decimal.NewFromInt(1)).Mul(tenK)
	}
	return Opportunity{
		ID:             id,
		Exchange:       exch,
		TriangleID:     quote.Triangle,
		Start:          quote.Start,
		Quote:          quote,
		Buffers:        b,
		BufferAmount:   bufferAmt,
		EstimatedFinal: estFinal,
		NetProfit:      netProfit,
		NetReturnBps:   netBps,
		GrossReturnBps: quote.ReturnBps,
		RecommendedSize: input,
		DetectedAt:     now,
		ExpiresAt:      now.Add(ttl),
		ConfigVersion:  configVersion,
		Status:         StatusDetected,
	}
}

// Expired reports TTL expiry.
func (o *Opportunity) Expired(now time.Time) bool { return now.After(o.ExpiresAt) }

// Transition moves the opportunity to a new status, enforcing the table.
// Terminal states never transition again.
func (o *Opportunity) Transition(to Status, reason string) error {
	for _, allowed := range legalTransitions[o.Status] {
		if allowed == to {
			o.Status = to
			if reason != "" {
				o.Reason = reason
			}
			return nil
		}
	}
	return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, o.Status, to)
}

// Expire is the TTL path: only QUALIFIED and RESERVED can expire.
func (o *Opportunity) Expire(now time.Time) error {
	if !o.Expired(now) {
		return fmt.Errorf("opportunity %s not yet expired", o.ID)
	}
	if o.Status != StatusQualified && o.Status != StatusReserved {
		return fmt.Errorf("%w: %s", ErrNotExpirable, o.Status)
	}
	o.Status = StatusExpired
	o.Reason = "TTL_EXPIRED"
	return nil
}

// BookVersions returns the per-leg book versions the quote was computed
// from — the revalidation contract compares these against fresh views.
func (o *Opportunity) BookVersions() [3]uint64 {
	return [3]uint64{
		o.Quote.Legs[0].BookVersion,
		o.Quote.Legs[1].BookVersion,
		o.Quote.Legs[2].BookVersion,
	}
}

// NeedsRecalc reports whether any leg's book moved past the recorded
// version. Any movement is "material" by default: recomputing a cycle is
// microseconds, guessing is how false positives survive (SKILL.md §20).
func (o *Opportunity) NeedsRecalc(current [3]uint64) bool {
	recorded := o.BookVersions()
	for i := range recorded {
		if current[i] != recorded[i] {
			return true
		}
	}
	return false
}
