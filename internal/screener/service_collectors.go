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
	RateLimited   int        `json:"rate_limited"`
	Polls         int64      `json:"polls"`
	LastError     string     `json:"error,omitempty"`
}

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
