package screener

import (
	"context"
	"time"
)

// VenueStatus is one venue collector's live state as GET /screener/status
// reports it (T-066). It lives in this package (not venue/) so the API
// layer and the Service can read it without importing the collectors.
type VenueStatus struct {
	ID            Venue      `json:"id"`
	Enabled       bool       `json:"enabled"`
	Online        bool       `json:"online"`
	LastPollAt    *time.Time `json:"last_poll_at"`
	PollMS        int64      `json:"poll_ms"`
	SpotPairs     int        `json:"spot_pairs"`
	PerpContracts int        `json:"perp_contracts"`
	// PerpsDropped is how many perp contracts the last poll discarded
	// because the venue listed more than one contract on the same base
	// (different quote/margin assets) and the suite tracks one per
	// (venue, base) — settings.perp_quote_preference picks it. The
	// dropped contracts are logged once each; this is the running count
	// an operator sees in GET /screener/status.
	PerpsDropped int   `json:"perps_dropped"`
	RateLimited  int   `json:"rate_limited"`
	Polls        int64 `json:"polls"`
	// Restarts counts how many times the self-healing loop (T-079,
	// Automation.healCollectors) replaced this venue's goroutine because
	// it had not completed a poll for 5 × poll_interval_s.
	Restarts  int64  `json:"restarts"`
	LastError string `json:"error,omitempty"`
}

// StaleRestarter is the optional CollectorRunner extension the
// Automation loop uses for self-healing: RestartStale replaces every
// venue loop whose last completed poll (or start) is older than maxAge
// and returns the venues it restarted. venue.Poller implements it.
type StaleRestarter interface {
	RestartStale(now time.Time, maxAge time.Duration) []Venue
}

// StaleAfterPolls is the self-healing threshold in poll intervals.
const StaleAfterPolls = 5

// CollectorRunner is what venue.Poller implements; the Service only
// needs start/stop/status.
type CollectorRunner interface {
	Start(ctx context.Context) error
	Stop()
	Running() bool
	Status() []VenueStatus
}

// StartCollectors starts the attached runner (no-op when none is wired,
// e.g. tests and DB-less profiles that never call SetCollectors).
func (s *Service) StartCollectors(ctx context.Context) error {
	if s.Collectors == nil {
		return nil
	}
	return s.Collectors.Start(ctx)
}

// StopCollectors stops the attached runner, if any.
func (s *Service) StopCollectors() {
	if s.Collectors != nil {
		s.Collectors.Stop()
	}
}

// CollectorStatus returns ("running"|"stopped"|"not_started", per-venue
// status). "not_started" means no runner is wired at all — the honest
// pre-T-066 answer the status endpoint has always given.
func (s *Service) CollectorStatus() (string, []VenueStatus) {
	if s.Collectors == nil {
		return "not_started", nil
	}
	if !s.Collectors.Running() {
		return "stopped", s.Collectors.Status()
	}
	return "running", s.Collectors.Status()
}
