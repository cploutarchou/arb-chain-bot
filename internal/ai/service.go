package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// PromptVersion pins the template; analyses are reproducible from the
// stored input + this version.
const PromptVersion = "v1"

// buildPrompt renders the versioned template. The input is our own
// typed summary serialized to JSON — no untrusted text enters here.
func buildPrompt(in Input) (string, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`You are the analytical advisor for a triangular-arbitrage paper-trading platform. You never execute anything; a deterministic risk engine gates all activity and humans approve every change.

Analyze the %s snapshot below (typed aggregates from the platform itself).

Respond with EXACTLY one JSON object, no markdown, matching:
{
  "summary": "<= 2000 chars",
  "findings": ["... up to 12 short strings ..."],
  "recommendations": [
    {
      "parameter": "<dotted path that exists under scanner.*, risk.* or notifications.* in the input's params>",
      "recommended_value": "<new value as a string>",
      "evidence": "<metric-based justification>",
      "reason": "<why>",
      "confidence": "<decimal 0..1>",
      "expected_effect": "<what should improve>",
      "risks": "<what could get worse>"
    }
  ]
}
Up to 4 recommendations; propose none unless the data supports them.

INPUT:
%s`, in.Kind, payload), nil
}

// Store persists analyses and recommendation lifecycle (nil = memory only).
type Store interface {
	InsertAnalysis(ctx context.Context, res AnalysisResult, input json.RawMessage) error
	InsertRecommendation(ctx context.Context, rec Recommendation) error
	UpdateRecommendationStatus(ctx context.Context, id, status, decidedBy string, decidedAt time.Time) error
}

// ErrRecommendationNotFound / ErrRecommendationDecided guard decisions.
var (
	ErrRecommendationNotFound = errors.New("ai: recommendation not found")
	ErrRecommendationDecided  = errors.New("ai: recommendation already decided or expired")
)

// Service runs analyses and owns the recommendation lifecycle. Provider
// failures never propagate beyond an error + WARNING alert — the
// scanner and paper engine do not depend on this service.
type Service struct {
	Advisor  Advisor
	Store    Store
	Strategy *strategy.Service
	Notify   func(notification.Event)
	Log      *slog.Logger
	IDGen    func() string
	Now      func() time.Time
	// Timeout bounds one provider call (default 60s).
	Timeout time.Duration
	// Limits, when set, is read at every RunAnalysis for the daily cap
	// (settings-expansion §4.1). The counter is per process and per UTC
	// day — not persisted, so a process restart resets it.
	Limits func() Budget

	once     sync.Once
	mu       sync.Mutex
	analyses []AnalysisResult // ring, newest last
	recs     map[string]*Recommendation
	dayKey   string // UTC date of the current daily counter
	dayCount int
	lastAt   time.Time

	requests atomic.Int64
	failures atomic.Int64
}

// ErrBudgetExhausted is returned when MaxAnalysesPerDay has been reached
// for the current UTC day. Like ErrAdvisorDisabled it is a skip, not a
// failure: logged at INFO, never counted or alerted.
var ErrBudgetExhausted = errors.New("ai: daily analysis budget exhausted")

// Usage is the advisor's runtime status line for the console.
type Usage struct {
	AnalysesToday int        `json:"analyses_today"`
	MaxPerDay     int        `json:"max_per_day"` // 0 = uncapped
	LastAnalysis  *time.Time `json:"last_analysis,omitempty"`
}

// Usage reports today's counter (UTC), the cap in force, and the last
// successful analysis time.
func (s *Service) Usage() Usage {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	u := Usage{}
	if s.dayKey == s.Now().UTC().Format("2006-01-02") {
		u.AnalysesToday = s.dayCount
	}
	if s.Limits != nil {
		u.MaxPerDay = s.Limits().MaxAnalysesPerDay
	}
	if !s.lastAt.IsZero() {
		at := s.lastAt
		u.LastAnalysis = &at
	}
	return u
}

// reserveBudget increments today's counter under the cap, resetting on
// UTC-day rollover. Returns false when the cap is reached.
func (s *Service) reserveBudget() bool {
	max := 0
	if s.Limits != nil {
		max = s.Limits().MaxAnalysesPerDay
	}
	today := s.Now().UTC().Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dayKey != today {
		s.dayKey, s.dayCount = today, 0
	}
	if max > 0 && s.dayCount >= max {
		return false
	}
	s.dayCount++
	return true
}

const analysesCap = 32

// init is called from every entry point; sync.Once makes the lazy
// defaults race-free when the first calls arrive concurrently (audit
// CR-P1-4 — the unguarded version could double-create the map and
// write Now/Timeout while another goroutine read them).
func (s *Service) init() {
	s.once.Do(func() {
		s.recs = map[string]*Recommendation{}
		if s.Now == nil {
			s.Now = time.Now
		}
		if s.Timeout <= 0 {
			s.Timeout = 60 * time.Second
		}
	})
}

// Requests / Failures back ai_requests_total / ai_failures_total.
func (s *Service) Requests() int64 { return s.requests.Load() }
func (s *Service) Failures() int64 { return s.failures.Load() }

// RunAnalysis executes one advisor call end to end: prompt → provider →
// strict validation → persist → notify. Every failure path increments
// the failure counter, raises a WARNING, and returns an error the
// caller may ignore (scheduled runs do).
func (s *Service) RunAnalysis(ctx context.Context, in Input) (AnalysisResult, error) {
	s.init()
	// Disabled is not a failure (D4): return before the request counter,
	// the failure counter and the notifier are touched.
	if sw, ok := s.Advisor.(interface{ Enabled() bool }); ok && !sw.Enabled() {
		return AnalysisResult{}, ErrAdvisorDisabled
	}
	if !s.reserveBudget() {
		s.Log.Info("ai analysis skipped: daily budget exhausted", "kind", string(in.Kind))
		return AnalysisResult{}, ErrBudgetExhausted
	}
	s.requests.Add(1)
	prompt, err := buildPrompt(in)
	if err != nil {
		return AnalysisResult{}, s.fail("prompt build failed", err)
	}
	s.Log.Info("ai analysis starting",
		"kind", string(in.Kind), "model", s.Advisor.Model(),
		"prompt_version", PromptVersion, "config_version", in.ConfigVersion)

	callCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	raw, err := s.Advisor.Analyze(callCtx, prompt)
	if errors.Is(err, ErrAdvisorDisabled) {
		// Switched off between the check above and the call: still a
		// skip, not a failure.
		return AnalysisResult{}, ErrAdvisorDisabled
	}
	if err != nil {
		return AnalysisResult{}, s.fail("provider call failed", err)
	}
	current := in.Params
	resp, discarded, err := parseResponse(raw, current)
	if err != nil {
		return AnalysisResult{}, s.fail("output rejected", err)
	}
	for _, reason := range discarded {
		s.Log.Warn("ai recommendation discarded", "reason", reason)
	}
	resp.Findings = append(resp.Findings, discarded...)

	now := s.Now().UTC()
	res := AnalysisResult{
		ID: s.IDGen(), Kind: in.Kind, At: now,
		Model: s.Advisor.Model(), PromptVersion: PromptVersion,
		ConfigVersion: in.ConfigVersion,
		Summary:       resp.Summary, Findings: resp.Findings,
		Raw: json.RawMessage(raw),
	}
	for _, r := range resp.Recommendations {
		cur := currentValueAt(current, r.Parameter)
		rec := Recommendation{
			ID: s.IDGen(), AnalysisID: res.ID, CreatedAt: now,
			Scope: "global", Parameter: r.Parameter,
			CurrentValue: cur, RecommendedValue: r.RecommendedValue,
			Evidence: r.Evidence, Reason: r.Reason, Confidence: r.Confidence,
			ExpectedEffect: r.ExpectedEffect, Risks: r.Risks,
			ExpiresAt: now.Add(recExpiry), Status: "proposed",
		}
		res.Recommendations = append(res.Recommendations, rec)
	}

	s.mu.Lock()
	s.lastAt = now
	s.analyses = append(s.analyses, res)
	if len(s.analyses) > analysesCap {
		s.analyses = s.analyses[len(s.analyses)-analysesCap:]
	}
	for i := range res.Recommendations {
		rec := res.Recommendations[i]
		s.recs[rec.ID] = &rec
	}
	s.mu.Unlock()

	if s.Store != nil {
		inputJSON, _ := json.Marshal(in)
		persistCtx, cancelP := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancelP()
		if err := s.Store.InsertAnalysis(persistCtx, res, inputJSON); err != nil {
			s.Log.Error("ai analysis persist failed", "error", err)
		}
		for _, rec := range res.Recommendations {
			if err := s.Store.InsertRecommendation(persistCtx, rec); err != nil {
				s.Log.Error("ai recommendation persist failed", "rec_id", rec.ID, "error", err)
			}
		}
	}
	if s.Notify != nil {
		s.Notify(notification.Event{
			Severity: notification.SeverityInfo, Key: "ai:analysis:" + string(in.Kind),
			Title: "AI analysis complete",
			Body: fmt.Sprintf("%s: %s (%d recommendation(s))",
				in.Kind, truncate(res.Summary, 160), len(res.Recommendations)),
		})
	}
	s.Log.Info("ai analysis complete", "analysis_id", res.ID,
		"kind", string(in.Kind), "recommendations", len(res.Recommendations))
	return res, nil
}

func (s *Service) fail(msg string, err error) error {
	s.failures.Add(1)
	s.Log.Warn("ai "+msg, "error", err)
	if s.Notify != nil {
		s.Notify(notification.Event{
			Severity: notification.SeverityWarning, Key: "ai:failure",
			Title: "AI advisor failure", Body: msg + ": " + err.Error(),
		})
	}
	return fmt.Errorf("ai: %s: %w", msg, err)
}

// Analyses returns recent results, newest first.
func (s *Service) Analyses(limit int) []AnalysisResult {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.analyses) {
		limit = len(s.analyses)
	}
	out := make([]AnalysisResult, 0, limit)
	for i := len(s.analyses) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.analyses[i])
	}
	return out
}

// Recommendations lists by status ("" = all), newest first. Rejection
// history is part of the record and never pruned.
func (s *Service) Recommendations(status string) []Recommendation {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Recommendation, 0, len(s.recs))
	for _, r := range s.recs {
		if status != "" && r.Status != status {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Approve applies the recommendation through the strategy service (the
// same validated, versioned, audited path as a human edit) and marks it
// approved. actor is the platform user ID. authorize is the approver's
// per-section RBAC gate, evaluated inside the strategy writer lock —
// an AI recommendation must never grant a permission its approver does
// not hold (audit S-002/P0-2: an OPERATOR could previously approve a
// risk.* change that the config API reserves for ADMIN). nil authorize
// is for trusted internal callers only.
func (s *Service) Approve(ctx context.Context, id, actor, source string, authorize strategy.Authorize) (strategy.Snapshot, error) {
	s.init()
	s.mu.Lock()
	rec, ok := s.recs[id]
	if !ok {
		s.mu.Unlock()
		return strategy.Snapshot{}, ErrRecommendationNotFound
	}
	if rec.Status != "proposed" || rec.deciding || s.Now().After(rec.ExpiresAt) {
		s.mu.Unlock()
		return strategy.Snapshot{}, ErrRecommendationDecided
	}
	rec.deciding = true // hold the reservation across the unlocked apply
	parameter, value := rec.Parameter, rec.RecommendedValue
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		if r, ok := s.recs[id]; ok {
			r.deciding = false
		}
		s.mu.Unlock()
	}

	next, err := strategy.ApplyChange(s.Strategy.Current().Params, parameter, coerceValue(value))
	if err != nil {
		release()
		return strategy.Snapshot{}, err
	}
	snap, err := s.Strategy.ApplyAuthorized(ctx, actor, source, next, authorize)
	if err != nil {
		release()
		return strategy.Snapshot{}, err
	}
	s.decide(ctx, id, "approved", actor)
	if s.Notify != nil {
		s.Notify(notification.Event{
			Severity: notification.SeverityInfo, Key: "ai:decision",
			Title: "AI recommendation approved",
			Body:  fmt.Sprintf("%s → %s (config v%d)", parameter, value, snap.Version),
		})
	}
	return snap, nil
}

// Reject marks a proposed recommendation rejected; the record stays.
func (s *Service) Reject(ctx context.Context, id, actor string) error {
	s.init()
	s.mu.Lock()
	rec, ok := s.recs[id]
	if !ok {
		s.mu.Unlock()
		return ErrRecommendationNotFound
	}
	if rec.Status != "proposed" || rec.deciding {
		s.mu.Unlock()
		return ErrRecommendationDecided
	}
	s.mu.Unlock()
	s.decide(ctx, id, "rejected", actor)
	return nil
}

func (s *Service) decide(ctx context.Context, id, status, actor string) {
	now := s.Now().UTC()
	s.mu.Lock()
	if rec, ok := s.recs[id]; ok {
		rec.Status = status
		rec.DecidedBy = actor
		rec.DecidedAt = now
	}
	s.mu.Unlock()
	if s.Store != nil {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.Store.UpdateRecommendationStatus(pctx, id, status, actor, now); err != nil {
			s.Log.Error("ai recommendation status persist failed", "rec_id", id, "error", err)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary so notification bodies stay valid UTF-8.
	cut := n - 1
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func currentValueAt(p strategy.Params, path string) string {
	raw, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return ""
	}
	var node any = tree
	for _, seg := range splitPath(path) {
		m, ok := node.(map[string]any)
		if !ok {
			return ""
		}
		node = m[seg]
	}
	switch v := node.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%g", v)
	default:
		b, _ := json.Marshal(node)
		return string(b)
	}
}

func splitPath(p string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '.' {
			if i > start {
				out = append(out, p[start:i])
			}
			start = i + 1
		}
	}
	return out
}

// Scheduler runs the standing analyses (hourly health, daily
// performance, weekly parameter review). A failed run alerts and waits
// for the next tick; it never stops the loop and never touches the
// trading path.
//
// Cadence, when set, is read at every wake (settings-expansion §4.1), so
// a cadence change applies without a restart: a re-armable timer loop
// replaces the three fixed tickers. A zero cadence disarms that kind.
// Hourly/Daily/Weekly are the fallback when Cadence is nil (tests and
// callers that predate the settings document).
type Scheduler struct {
	Service *Service
	// InputFor builds the typed summary at fire time.
	InputFor func(kind AnalysisKind) Input
	Log      *slog.Logger

	Hourly time.Duration // defaults: 1h / 24h / 7d
	Daily  time.Duration
	Weekly time.Duration

	Cadence func() Cadence
	// Wake bounds how long the loop sleeps before re-reading Cadence
	// (default 1 minute), so a shortened cadence takes effect within
	// that bound rather than only after the previously-armed interval.
	Wake time.Duration

	// now is the clock (tests inject a fake to drive minute-scale
	// cadences without waiting); nil = time.Now.
	now func() time.Time
}

func (s *Scheduler) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Scheduler) Name() string { return "ai-scheduler" }

// intervals resolves the three cadences in force right now; 0 = off.
func (s *Scheduler) intervals() [3]time.Duration {
	if s.Cadence != nil {
		c := s.Cadence()
		return [3]time.Duration{
			time.Duration(c.HourlyMinutes) * time.Minute,
			time.Duration(c.DailyHours) * time.Hour,
			time.Duration(c.WeeklyHours) * time.Hour,
		}
	}
	h, d, w := s.Hourly, s.Daily, s.Weekly
	if h <= 0 {
		h = time.Hour
	}
	if d <= 0 {
		d = 24 * time.Hour
	}
	if w <= 0 {
		w = 7 * 24 * time.Hour
	}
	return [3]time.Duration{h, d, w}
}

func (s *Scheduler) Run(ctx context.Context) error {
	kinds := [3]AnalysisKind{KindHourlyHealth, KindDailyPerformance, KindWeeklyParameters}
	wake := s.Wake
	if wake <= 0 {
		wake = time.Minute
	}
	// last[i] is when kind i last fired (or the loop start), so the first
	// fire of each kind lands one full interval after start — the same
	// phase a ticker had.
	var last [3]time.Time
	now := s.clock()
	for i := range last {
		last[i] = now
	}
	timer := time.NewTimer(wake)
	defer timer.Stop()
	for {
		iv := s.intervals()
		now = s.clock()
		sleep := wake
		for i := range kinds {
			if iv[i] <= 0 {
				continue
			}
			due := last[i].Add(iv[i])
			if !due.After(now) {
				last[i] = now
				if _, err := s.Service.RunAnalysis(ctx, s.InputFor(kinds[i])); err != nil {
					// Already counted, logged, and alerted by the service
					// (or a skip: disabled/budget).
					s.Log.Debug("scheduled analysis did not run; next tick continues", "kind", string(kinds[i]), "reason", err.Error())
				}
				now = s.clock()
				due = last[i].Add(iv[i])
			}
			if d := due.Sub(now); d < sleep {
				sleep = d
			}
		}
		if sleep < 0 {
			sleep = 0
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(sleep)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
