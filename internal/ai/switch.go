package ai

import (
	"context"
	"errors"
	"sync/atomic"
)

// ErrAdvisorDisabled is returned by Switch.Analyze (and, early, by
// Service.RunAnalysis) when no advisor is installed. Disabled is not a
// failure: RunAnalysis returns it without touching the failure counter
// or the notifier — the old "build nothing when no key" wiring would
// otherwise have turned a runtime enable into an hourly WARNING loop.
var ErrAdvisorDisabled = errors.New("ai: advisor disabled")

// Switch is an Advisor over an atomic, possibly-nil advisor (design D4):
// ai.Service and ai.Scheduler are always constructed with the Switch as
// their Advisor, and the platform-settings subscriber swaps the real
// provider in and out at runtime. Idle cost is one timer that skips.
type Switch struct {
	p atomic.Pointer[advisorBox]
}

type advisorBox struct{ adv Advisor }

// Set installs adv (nil disables). Safe to call concurrently with
// Analyze; an in-flight call keeps the advisor it started with.
func (s *Switch) Set(adv Advisor) {
	if adv == nil {
		s.p.Store(nil)
		return
	}
	s.p.Store(&advisorBox{adv: adv})
}

// Current returns the installed advisor, or nil.
func (s *Switch) Current() Advisor {
	if b := s.p.Load(); b != nil {
		return b.adv
	}
	return nil
}

// Enabled reports whether an advisor is installed.
func (s *Switch) Enabled() bool { return s.p.Load() != nil }

func (s *Switch) Name() string {
	if a := s.Current(); a != nil {
		return a.Name()
	}
	return "disabled"
}

func (s *Switch) Model() string {
	if a := s.Current(); a != nil {
		return a.Model()
	}
	return ""
}

func (s *Switch) Analyze(ctx context.Context, prompt string) (string, error) {
	a := s.Current()
	if a == nil {
		return "", ErrAdvisorDisabled
	}
	return a.Analyze(ctx, prompt)
}

// Cadence mirrors platform.AISchedule without importing it (internal/ai
// must stay independent of the settings document): 0 disables a kind.
type Cadence struct {
	HourlyMinutes int
	DailyHours    int
	WeeklyHours   int
}

// Budget mirrors platform.AIBudget. Zero values mean "no cap" so a
// caller that never wires Limits keeps today's behaviour.
type Budget struct {
	MaxAnalysesPerDay int
	MaxOutputTokens   int
}
