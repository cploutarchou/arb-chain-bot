package entitlements

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// Exceeded is the 403 entitlement_exceeded payload: the schema key that
// was breached and a message a tenant can act on.
type Exceeded struct {
	Key     string
	Limit   any
	Message string
}

func (e *Exceeded) Error() string { return "entitlement exceeded: " + e.Key + ": " + e.Message }

// ErrExceeded lets callers errors.Is without the concrete type.
var ErrExceeded = errors.New("entitlement_exceeded")

func (e *Exceeded) Is(target error) bool { return target == ErrExceeded }

func exceeded(key string, limit any, format string, args ...any) error {
	return &Exceeded{Key: key, Limit: limit, Message: fmt.Sprintf(format, args...)}
}

// CheckRuleCount enforces rules.max_active on creation (active = the
// number of rules that already exist for the organisation).
func (e Entitlements) CheckRuleCount(existing int) error {
	if e.Rules.MaxActive != Unlimited && existing >= e.Rules.MaxActive {
		return exceeded("rules.max_active", e.Rules.MaxActive, "your package allows %d alert rules; delete one or upgrade", e.Rules.MaxActive)
	}
	return nil
}

// CheckTemplateCount enforces rules.templates_max.
func (e Entitlements) CheckTemplateCount(existing int) error {
	if e.Rules.TemplatesMax != Unlimited && existing >= e.Rules.TemplatesMax {
		return exceeded("rules.templates_max", e.Rules.TemplatesMax, "your package allows %d saved templates", e.Rules.TemplatesMax)
	}
	return nil
}

// CheckRuleKind enforces rules.kinds.
func (e Entitlements) CheckRuleKind(kind string) error {
	if !Has(e.Rules.Kinds, kind) {
		return exceeded("rules.kinds", e.Rules.Kinds, "rule kind %q is not included in your package", kind)
	}
	return nil
}

// CheckRefresh enforces rules.min_refresh_s on the screener poll interval.
func (e Entitlements) CheckRefresh(seconds int) error {
	if seconds < e.Rules.MinRefreshS {
		return exceeded("rules.min_refresh_s", e.Rules.MinRefreshS, "your package's lowest refresh interval is %d s", e.Rules.MinRefreshS)
	}
	return nil
}

// CheckCooldown enforces alerts.min_cooldown_s.
func (e Entitlements) CheckCooldown(seconds int64) error {
	if seconds < int64(e.Alerts.MinCooldownS) {
		return exceeded("alerts.min_cooldown_s", e.Alerts.MinCooldownS, "your package's lowest alert cooldown is %d s", e.Alerts.MinCooldownS)
	}
	return nil
}

// CheckChannel enforces alerts.channels.
func (e Entitlements) CheckChannel(channel string) error {
	if !Has(e.Alerts.Channels, channel) {
		return exceeded("alerts.channels", e.Alerts.Channels, "alert channel %q is not included in your package", channel)
	}
	return nil
}

// CheckVenues enforces venues.screener_fixed (locked set) and
// venues.screener_max (distinct venue count).
func (e Entitlements) CheckVenues(venues []string) error {
	distinct := map[string]bool{}
	for _, v := range venues {
		distinct[v] = true
		if len(e.Venues.ScreenerFixed) > 0 && !Has(e.Venues.ScreenerFixed, v) {
			return exceeded("venues.screener_fixed", e.Venues.ScreenerFixed, "venue %q is not in your package's fixed venue set %v", v, e.Venues.ScreenerFixed)
		}
	}
	if e.Venues.ScreenerMax != Unlimited && len(distinct) > e.Venues.ScreenerMax {
		return exceeded("venues.screener_max", e.Venues.ScreenerMax, "your package allows %d venues; %d selected", e.Venues.ScreenerMax, len(distinct))
	}
	return nil
}

// CheckAutoPaper enforces auto_paper.strategies and max_size_quote
// (decimal compare, never float) for a rule that asks for automatic
// paper execution.
func (e Entitlements) CheckAutoPaper(strategy string, sizeQuote decimal.Decimal) error {
	if len(e.AutoPaper.Strategies) == 0 {
		return exceeded("auto_paper.strategies", e.AutoPaper.Strategies, "automatic paper execution is not included in your package (manual paper only)")
	}
	if strategy != "" && !Has(e.AutoPaper.Strategies, strategy) {
		return exceeded("auto_paper.strategies", e.AutoPaper.Strategies, "auto-paper strategy %q is not included in your package", strategy)
	}
	maxSize, err := decimal.NewFromString(e.AutoPaper.MaxSizeQuote)
	if err != nil {
		return fmt.Errorf("%w: max_size_quote: %v", ErrInvalid, err)
	}
	if sizeQuote.GreaterThan(maxSize) {
		return exceeded("auto_paper.max_size_quote", e.AutoPaper.MaxSizeQuote, "paper size %s exceeds your package cap of %s", sizeQuote.String(), maxSize.String())
	}
	return nil
}

// CheckOpenPositions enforces auto_paper.max_open_positions.
func (e Entitlements) CheckOpenPositions(open int) error {
	if open >= e.AutoPaper.MaxOpenPositions {
		return exceeded("auto_paper.max_open_positions", e.AutoPaper.MaxOpenPositions, "your package allows %d concurrent auto-paper positions", e.AutoPaper.MaxOpenPositions)
	}
	return nil
}

// CheckSeat enforces seats.max and seats.roles on an invitation or
// role change. role is the lowercase schema role name.
func (e Entitlements) CheckSeat(members int, role string) error {
	if members >= e.Seats.Max {
		return exceeded("seats.max", e.Seats.Max, "your package includes %d seat(s)", e.Seats.Max)
	}
	return e.CheckSeatRole(role)
}

// CheckSeatRole enforces seats.roles.
func (e Entitlements) CheckSeatRole(role string) error {
	if !Has(e.Seats.Roles, role) {
		return exceeded("seats.roles", e.Seats.Roles, "role %q is not included in your package", role)
	}
	return nil
}

// CheckAPIScope enforces api.enabled and api.scopes for API-key callers.
func (e Entitlements) CheckAPIScope(scope string) error {
	if !e.API.Enabled {
		return exceeded("api.enabled", false, "client API access is not included in your package")
	}
	if !Has(e.API.Scopes, scope) {
		return exceeded("api.scopes", e.API.Scopes, "API scope %q is not included in your package", scope)
	}
	return nil
}

// RetentionCutoff is the oldest timestamp visible under
// history.retention_days.
func (e Entitlements) RetentionCutoff(now time.Time) time.Time {
	return now.Add(-time.Duration(e.History.RetentionDays) * 24 * time.Hour)
}

// DailyCounter enforces alerts.per_day per organisation and UTC day
// (in-process; the Redis counter in packages.md §3.2 replaces it when
// the dispatcher is scaled out). Allow returns false once the quota is
// used up for the day; perDay == Unlimited (-1) never refuses.
type DailyCounter struct {
	mu   sync.Mutex
	day  string
	used map[int64]int
}

func NewDailyCounter() *DailyCounter { return &DailyCounter{used: map[int64]int{}} }

func (c *DailyCounter) Allow(orgID int64, perDay int, now time.Time) bool {
	if perDay == Unlimited {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	day := now.UTC().Format("2006-01-02")
	if day != c.day {
		c.day = day
		c.used = map[int64]int{}
	}
	if c.used[orgID] >= perDay {
		return false
	}
	c.used[orgID]++
	return true
}

// Used reports today's consumed quota for orgID (diagnostics/tests).
func (c *DailyCounter) Used(orgID int64, now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.UTC().Format("2006-01-02") != c.day {
		return 0
	}
	return c.used[orgID]
}

// RateLimiter is a per-key token bucket for api.rate_per_min / burst
// (in-process; see DailyCounter for the scale-out note). Allow returns
// ok=false and the seconds to wait when the bucket is empty.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewRateLimiter() *RateLimiter { return &RateLimiter{buckets: map[string]*bucket{}} }

func (l *RateLimiter) Allow(key string, ratePerMin, burst int, now time.Time) (ok bool, retryAfter time.Duration) {
	if ratePerMin <= 0 {
		return false, time.Minute
	}
	if burst <= 0 {
		burst = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, exists := l.buckets[key]
	if !exists {
		b = &bucket{tokens: float64(burst), last: now}
		l.buckets[key] = b
	}
	perSec := float64(ratePerMin) / 60
	b.tokens += now.Sub(b.last).Seconds() * perSec
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / perSec * float64(time.Second))
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}
