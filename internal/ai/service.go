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

	mu       sync.Mutex
	analyses []AnalysisResult // ring, newest last
	recs     map[string]*Recommendation

	requests atomic.Int64
	failures atomic.Int64
}

const analysesCap = 32

func (s *Service) init() {
	if s.recs == nil {
		s.recs = map[string]*Recommendation{}
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Timeout <= 0 {
		s.Timeout = 60 * time.Second
	}
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
	if err != nil {
		return AnalysisResult{}, s.fail("provider call failed", err)
	}
	current := in.Params
	resp, err := parseResponse(raw, current)
	if err != nil {
		return AnalysisResult{}, s.fail("output rejected", err)
	}

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
// approved. actor is the platform user ID.
func (s *Service) Approve(ctx context.Context, id, actor, source string) (strategy.Snapshot, error) {
	s.init()
	s.mu.Lock()
	rec, ok := s.recs[id]
	if !ok {
		s.mu.Unlock()
		return strategy.Snapshot{}, ErrRecommendationNotFound
	}
	if rec.Status != "proposed" || s.Now().After(rec.ExpiresAt) {
		s.mu.Unlock()
		return strategy.Snapshot{}, ErrRecommendationDecided
	}
	parameter, value := rec.Parameter, rec.RecommendedValue
	s.mu.Unlock()

	next, err := strategy.ApplyChange(s.Strategy.Current().Params, parameter, coerceValue(value))
	if err != nil {
		return strategy.Snapshot{}, err
	}
	snap, err := s.Strategy.Apply(ctx, actor, source, next)
	if err != nil {
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
	if rec.Status != "proposed" {
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
	return s[:n-1] + "…"
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
type Scheduler struct {
	Service *Service
	// InputFor builds the typed summary at fire time.
	InputFor func(kind AnalysisKind) Input
	Log      *slog.Logger

	Hourly time.Duration // defaults: 1h / 24h / 7d
	Daily  time.Duration
	Weekly time.Duration
}

func (s *Scheduler) Name() string { return "ai-scheduler" }

func (s *Scheduler) Run(ctx context.Context) error {
	if s.Hourly <= 0 {
		s.Hourly = time.Hour
	}
	if s.Daily <= 0 {
		s.Daily = 24 * time.Hour
	}
	if s.Weekly <= 0 {
		s.Weekly = 7 * 24 * time.Hour
	}
	hourly := time.NewTicker(s.Hourly)
	daily := time.NewTicker(s.Daily)
	weekly := time.NewTicker(s.Weekly)
	defer hourly.Stop()
	defer daily.Stop()
	defer weekly.Stop()
	for {
		var kind AnalysisKind
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-hourly.C:
			kind = KindHourlyHealth
		case <-daily.C:
			kind = KindDailyPerformance
		case <-weekly.C:
			kind = KindWeeklyParameters
		}
		if _, err := s.Service.RunAnalysis(ctx, s.InputFor(kind)); err != nil {
			// Already counted, logged, and alerted by the service.
			s.Log.Debug("scheduled analysis failed; next tick continues", "kind", string(kind))
		}
	}
}
