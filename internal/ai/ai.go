// Package ai is the advisory subsystem (resources/ai-advisor.md,
// docs/risk.md §6, docs/security.md §6). The advisor is an analyst off
// the hot path: inputs are typed summaries only (never raw books,
// secrets, or verbatim untrusted text), outputs are strictly
// schema-validated JSON, and nothing changes until a human approves a
// recommendation — the deterministic risk engine is never overridden.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// AnalysisKind names a scheduled analysis.
type AnalysisKind string

const (
	KindHourlyHealth     AnalysisKind = "hourly_health"
	KindDailyPerformance AnalysisKind = "daily_performance"
	KindWeeklyParameters AnalysisKind = "weekly_parameters"
)

// Input is the typed summary handed to the advisor. Every field is an
// aggregate produced by our own code; no raw market data, no free text
// from users, exchanges, or the web (prompt-injection boundary).
type Input struct {
	Kind          AnalysisKind    `json:"kind"`
	At            time.Time       `json:"at"`
	Mode          string          `json:"mode"`
	ConfigVersion int64           `json:"config_version"`
	Params        strategy.Params `json:"params"`

	Scanner ScannerSummary   `json:"scanner"`
	Feed    FeedSummary      `json:"feed"`
	Paper   *PaperSummary    `json:"paper,omitempty"`
	PnL     []AssetSummary   `json:"pnl,omitempty"`
	Rejects map[string]int64 `json:"reject_reason_counts,omitempty"`
	Alerts  AlertSummary     `json:"alerts"`
}

type ScannerSummary struct {
	Evaluations, Qualified, Rejected, SkippedBooks, DroppedEvents int64
}

type FeedSummary struct {
	Frames, Reconnects, APIErrors, Resyncs, SeqGaps int64
}

type PaperSummary struct {
	Received, Completed, Failed, Skipped int64
	Active                               int
}

type AssetSummary struct {
	Asset    string `json:"asset"`
	Realized string `json:"realized"`
	Fees     string `json:"fees"`
	Loss     string `json:"daily_loss"`
	Drawdown string `json:"drawdown"`
}

type AlertSummary struct {
	Active int `json:"active"`
}

// Recommendation is one proposed parameter change (docs/risk.md §6).
// It changes nothing until a human approves it.
type Recommendation struct {
	ID               string    `json:"id"`
	AnalysisID       string    `json:"analysis_id"`
	CreatedAt        time.Time `json:"created_at"`
	Scope            string    `json:"scope"`     // "global" (per-scope overrides arrive with the risk console)
	Parameter        string    `json:"parameter"` // dotted strategy path, e.g. "risk.min_net_edge_bps"
	CurrentValue     string    `json:"current_value"`
	RecommendedValue string    `json:"recommended_value"`
	Evidence         string    `json:"evidence"`
	Reason           string    `json:"reason"`
	Confidence       string    `json:"confidence"` // decimal string 0..1
	ExpectedEffect   string    `json:"expected_effect"`
	Risks            string    `json:"risks"`
	ExpiresAt        time.Time `json:"expires_at"`
	Status           string    `json:"status"` // proposed|approved|rejected|deferred|expired
	DecidedBy        string    `json:"decided_by,omitempty"`
	DecidedAt        time.Time `json:"decided_at,omitempty"`
}

// AnalysisResult is the validated outcome of one advisor run.
type AnalysisResult struct {
	ID              string           `json:"id"`
	Kind            AnalysisKind     `json:"kind"`
	At              time.Time        `json:"at"`
	Model           string           `json:"model"`
	PromptVersion   string           `json:"prompt_version"`
	ConfigVersion   int64            `json:"config_version"`
	Summary         string           `json:"summary"`
	Findings        []string         `json:"findings"`
	Recommendations []Recommendation `json:"recommendations"`
	Raw             json.RawMessage  `json:"-"`
}

// Advisor produces raw provider output for an input; the Service owns
// validation, so every provider (Anthropic, OpenAI, Fake) passes the
// same gate.
type Advisor interface {
	Name() string
	Model() string
	// Analyze returns the provider's raw JSON text response.
	Analyze(ctx context.Context, prompt string) (string, error)
}

// ---- strict response schema ---------------------------------------------

// response is the only shape the advisor may return. Unknown fields are
// rejected outright.
type response struct {
	Summary         string           `json:"summary"`
	Findings        []string         `json:"findings"`
	Recommendations []recommendation `json:"recommendations"`
}

type recommendation struct {
	Parameter        string `json:"parameter"`
	RecommendedValue string `json:"recommended_value"`
	Evidence         string `json:"evidence"`
	Reason           string `json:"reason"`
	Confidence       string `json:"confidence"`
	ExpectedEffect   string `json:"expected_effect"`
	Risks            string `json:"risks"`
}

// ErrInvalidOutput marks provider output that failed validation. AI
// text is untrusted input: nothing invalid gets through, ever.
var ErrInvalidOutput = errors.New("ai: provider output failed validation")

const (
	maxFindings   = 12
	maxRecs       = 4
	maxFieldLen   = 2000
	recExpiry     = 72 * time.Hour
	minConfidence = "0"
	maxConfidence = "1"
)

// parseResponse strictly decodes and bounds-checks the provider text.
// currentParams anchors parameter validation: a recommendation must
// name a known path and produce a payload that passes strategy
// validation when applied.
func parseResponse(raw string, currentParams strategy.Params) (response, error) {
	// Providers sometimes wrap JSON in markdown fences; strip exactly that.
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")

	var resp response
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&resp); err != nil {
		return response{}, fmt.Errorf("%w: %s", ErrInvalidOutput, err)
	}
	if dec.More() {
		return response{}, fmt.Errorf("%w: trailing content", ErrInvalidOutput)
	}
	if resp.Summary == "" || len(resp.Summary) > maxFieldLen {
		return response{}, fmt.Errorf("%w: summary empty or oversized", ErrInvalidOutput)
	}
	if len(resp.Findings) > maxFindings {
		return response{}, fmt.Errorf("%w: too many findings", ErrInvalidOutput)
	}
	for _, f := range resp.Findings {
		if len(f) > maxFieldLen {
			return response{}, fmt.Errorf("%w: finding oversized", ErrInvalidOutput)
		}
	}
	if len(resp.Recommendations) > maxRecs {
		return response{}, fmt.Errorf("%w: too many recommendations", ErrInvalidOutput)
	}
	for i, r := range resp.Recommendations {
		if err := validateRecommendation(r, currentParams); err != nil {
			return response{}, fmt.Errorf("%w: recommendation %d: %s", ErrInvalidOutput, i, err)
		}
	}
	return resp, nil
}

func validateRecommendation(r recommendation, current strategy.Params) error {
	for name, v := range map[string]string{
		"parameter": r.Parameter, "recommended_value": r.RecommendedValue,
		"reason": r.Reason, "confidence": r.Confidence,
	} {
		if v == "" {
			return fmt.Errorf("missing %s", name)
		}
	}
	for name, v := range map[string]string{
		"evidence": r.Evidence, "reason": r.Reason,
		"expected_effect": r.ExpectedEffect, "risks": r.Risks,
	} {
		if len(v) > maxFieldLen {
			return fmt.Errorf("%s oversized", name)
		}
	}
	conf, err := decimal.NewFromString(r.Confidence)
	if err != nil {
		return fmt.Errorf("confidence not a decimal: %v", err)
	}
	if conf.LessThan(decimal.RequireFromString(minConfidence)) ||
		conf.GreaterThan(decimal.RequireFromString(maxConfidence)) {
		return fmt.Errorf("confidence out of [0,1]")
	}
	// The change must land on a known parameter AND survive the same
	// validation gate as a human edit.
	if _, err := strategy.ApplyChange(current, r.Parameter, coerceValue(r.RecommendedValue)); err != nil {
		return fmt.Errorf("parameter %q rejected: %v", r.Parameter, err)
	}
	return nil
}

// coerceValue maps the provider's string to the JSON type the params
// tree expects: bare integers become numbers, everything else stays a
// string (decimal fields marshal as quoted strings).
func coerceValue(v string) any {
	var n int64
	if _, err := fmt.Sscanf(v, "%d", &n); err == nil && fmt.Sprintf("%d", n) == v {
		return n
	}
	return v
}
