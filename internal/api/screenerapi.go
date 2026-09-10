package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

func newScreenerID(prefix string) string {
	return prefix + "-" + ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// screenerVenueNames renders human-friendly labels for GET
// /screener/status; the id itself is the only thing anything else in
// this package keys off.
var screenerVenueNames = map[screener.Venue]string{
	screener.VenueBinance: "Binance",
	screener.VenueOKX:     "OKX",
	screener.VenueBybit:   "Bybit",
	screener.VenueBitget:  "Bitget",
	screener.VenueGate:    "Gate",
	screener.VenueMEXC:    "MEXC",
}

// screenerRoutes serves the Scanner Suite backend core (T-067/T-068,
// docs/design/scanner-suite.md §7). Collectors (T-066) are out of this
// task's scope: every read reflects the (currently empty) in-memory
// book honestly rather than pretending data exists.
func (s *Server) screenerRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Screener == nil {
				WriteError(w, http.StatusServiceUnavailable, "screener_unavailable", "screener not running in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	view := func(next http.HandlerFunc) http.HandlerFunc {
		return s.requirePerm(auth.PermScreenerView, gate(next))
	}
	// Every mutation lands in the caller's organisation; in the platform
	// organisation that takes platform_admin (requirePlatformScopeWrite):
	// the platform's screener document, rules and reports are the
	// operator's own, not any org-1 member's.
	config := func(next http.HandlerFunc) http.HandlerFunc {
		return s.requirePerm(auth.PermScreenerConfig, s.requirePlatformScopeWrite(s.requireCSRF(gate(next))))
	}

	mux.HandleFunc("GET /api/v1/screener/status", view(s.handleScreenerStatus))
	mux.HandleFunc("GET /api/v1/screener/spreads", view(s.handleScreenerSpreads))
	mux.HandleFunc("GET /api/v1/screener/perpetuals", view(s.handleScreenerPerpetuals))
	mux.HandleFunc("GET /api/v1/screener/funding", view(s.handleScreenerFunding))
	// The calculator computes against live top-of-book but persists
	// nothing; it is a read (PermScreenerView), CSRF-protected like
	// platform's preview route because it is still a POST.
	mux.HandleFunc("POST /api/v1/screener/calculator", s.requirePerm(auth.PermScreenerView, s.requireCSRF(gate(s.handleScreenerCalculator))))

	mux.HandleFunc("GET /api/v1/screener/settings", view(s.handleScreenerSettingsGet))
	mux.HandleFunc("POST /api/v1/screener/settings", config(s.handleScreenerSettingsApply))

	// Rule mutations additionally require rules:write for Bearer (API
	// key) callers (packages.md §3.1 api.scopes); requireAPIScope is a
	// no-op for session callers, who stay governed by PermScreenerConfig
	// alone, as before.
	mux.HandleFunc("GET /api/v1/screener/rules", view(s.handleScreenerRulesList))
	mux.HandleFunc("POST /api/v1/screener/rules", config(s.requireAPIScope("rules:write", s.handleScreenerRuleCreate)))
	mux.HandleFunc("PUT /api/v1/screener/rules/{id}", config(s.requireAPIScope("rules:write", s.handleScreenerRuleUpdate)))
	mux.HandleFunc("DELETE /api/v1/screener/rules/{id}", config(s.requireAPIScope("rules:write", s.handleScreenerRuleDelete)))

	mux.HandleFunc("GET /api/v1/screener/events", view(s.handleScreenerEvents))
	mux.HandleFunc("GET /api/v1/screener/auto-paper", view(s.handleScreenerAutoPaper))

	// T-078 nightly reports: reads are screener:view; the on-demand run
	// is ADMIN (screener:config) + CSRF + audit like every other
	// screener mutation. 503 when no generator runs in this profile.
	mux.HandleFunc("GET /api/v1/screener/reports", view(s.handleScreenerReportsList))
	mux.HandleFunc("GET /api/v1/screener/reports/{id}", view(s.handleScreenerReportGet))
	mux.HandleFunc("POST /api/v1/screener/reports/run", config(s.handleScreenerReportsRun))

	// Templates are per-user (design §7's route table tags them
	// "(per user)", NOT "(ADMIN)" the way every other mutation in this
	// group is tagged) — any caller who can view the screener may save
	// and delete their OWN filter presets, scoped by principal.UserID at
	// the storage layer. This mirrors usersapi.go's self-service
	// password change (requireAuth + requireCSRF only, no admin
	// permission): everyone manages their own row, nobody else's.
	mux.HandleFunc("GET /api/v1/screener/templates", view(s.handleScreenerTemplatesList))
	mux.HandleFunc("POST /api/v1/screener/templates", s.requirePerm(auth.PermScreenerView, s.requireCSRF(gate(s.requireAPIScope("templates:write", s.handleScreenerTemplateCreate)))))
	mux.HandleFunc("DELETE /api/v1/screener/templates/{id}", s.requirePerm(auth.PermScreenerView, s.requireCSRF(gate(s.requireAPIScope("templates:write", s.handleScreenerTemplateDelete)))))
}

// screenerSnapshot returns the active settings document of the caller's
// organisation (Service.SnapshotFor: the process-wide document for the
// platform organisation, the tenant's own otherwise). A store failure
// is answered 500 and reported as ok=false.
func (s *Server) screenerSnapshot(w http.ResponseWriter, r *http.Request) (screener.Snapshot, bool) {
	snap, err := s.Screener.SnapshotFor(r.Context())
	if err != nil {
		s.log.Error("screener settings load failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "settings_load_failed", "loading the screener settings failed", correlationID(r))
		return screener.Snapshot{}, false
	}
	return snap, true
}

func (s *Server) handleScreenerStatus(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.screenerSnapshot(w, r)
	if !ok {
		return
	}
	ids := screener.OrderedVenues
	collectors, live := s.Screener.CollectorStatus()
	liveBy := make(map[screener.Venue]screener.VenueStatus, len(live))
	for _, st := range live {
		liveBy[st.ID] = st
	}
	venues := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		vs, configured := snap.Settings.Venues[id]
		row := map[string]any{
			"id":             id,
			"name":           screenerVenueNames[id],
			"enabled":        configured && vs.Enabled,
			"online":         false,
			"last_poll_at":   nil,
			"poll_ms":        0,
			"spot_pairs":     0,
			"perp_contracts": 0,
			"rate_limited":   false,
			"error":          "collectors not started",
		}
		if st, ok := liveBy[id]; ok {
			row["online"] = st.Online
			row["last_poll_at"] = st.LastPollAt
			row["poll_ms"] = st.PollMS
			row["spot_pairs"] = st.SpotPairs
			row["perp_contracts"] = st.PerpContracts
			row["rate_limited"] = st.RateLimited > 0
			row["rate_limited_count"] = st.RateLimited
			row["perps_dropped"] = st.PerpsDropped
			row["polls"] = st.Polls
			row["restarts"] = st.Restarts
			row["error"] = st.LastError
		}
		venues = append(venues, row)
	}
	WriteData(w, http.StatusOK, map[string]any{
		"venues":          venues,
		"pairs_tracked":   len(s.Screener.Book.Pairs()),
		"spreads_per_sec": 0,
		"poll_interval_s": snap.Settings.PollIntervalS,
		"updated_at":      time.Now().UTC(),
		"collectors":      collectors,
		// Per-ticker counters (alerts: lanes evaluated / cut by the cap /
		// on hold) so an operator sees how much of the universe the
		// evaluator actually looked at; empty when no ticker registered.
		"automation": s.Screener.Diagnostics(),
	})
}

// screenerFeeLookup builds a screener.VenueFeeLookup over the given
// settings section (spot or perp taker bps), refusing a venue that is
// unconfigured or disabled for that section — spreads.go/basis.go both
// skip a lane rather than guess a fee when the lookup returns ok=false.
func screenerSpotFeeLookup(snap screener.Snapshot) screener.VenueFeeLookup {
	return func(v screener.Venue) (decimal.Decimal, bool) {
		vs, ok := snap.Settings.Venues[v]
		if !ok || !vs.Enabled {
			return decimal.Decimal{}, false
		}
		return vs.SpotTakerBps, true
	}
}

func screenerPerpFeeLookup(snap screener.Snapshot) screener.VenueFeeLookup {
	return func(v screener.Venue) (decimal.Decimal, bool) {
		vs, ok := snap.Settings.Venues[v]
		if !ok || !vs.Enabled || !vs.PerpsEnabled {
			return decimal.Decimal{}, false
		}
		return vs.PerpTakerBps, true
	}
}

func screenerSpotMidLookup(book *screener.Book) screener.SpotMidLookup {
	return func(venue screener.Venue, base, quote string) (decimal.Decimal, bool) {
		q, ok := book.QuotesFor(base, quote)[venue]
		if !ok || !q.Bid.IsPositive() || !q.Ask.IsPositive() {
			return decimal.Decimal{}, false
		}
		return q.Bid.Add(q.Ask).Div(decimal.NewFromInt(2)), true
	}
}

func parseVenueSet(csv string) map[screener.Venue]bool {
	if csv == "" {
		return nil
	}
	out := map[screener.Venue]bool{}
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[screener.Venue(part)] = true
		}
	}
	return out
}

func parseOptionalDecimal(w http.ResponseWriter, r *http.Request, param string) (*decimal.Decimal, bool) {
	raw := r.URL.Query().Get(param)
	if raw == "" {
		return nil, true
	}
	v, err := decimal.NewFromString(raw)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", param+" must be a decimal", correlationID(r))
		return nil, false
	}
	return &v, true
}

// handleScreenerSpreads serves GET /screener/spreads (design §3/§7).
// Defaults are the safe ones: suspect lanes (asset-identity guard) and
// unknown-liquidity lanes are excluded, and min_liquidity falls back to
// settings.min_liquidity_quote; include_suspect=1 /
// include_unknown_liquidity=1 opt back in and the excluded counts are
// always reported so the operator knows how many lanes were hidden.
func (s *Server) handleScreenerSpreads(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	minSpread, ok := parseOptionalDecimal(w, r, "min_spread_bps")
	if !ok {
		return
	}
	minLiquidity, ok := parseOptionalDecimal(w, r, "min_liquidity")
	if !ok {
		return
	}
	snap, ok := s.screenerSnapshot(w, r)
	if !ok {
		return
	}
	if minLiquidity == nil {
		v := snap.Settings.MinLiquidityQuote
		minLiquidity = &v
	}
	minLifetime, _ := strconv.ParseInt(q.Get("min_lifetime_s"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))

	f := screener.SpreadFilters{
		MinSpreadBpsNet:         minSpread,
		MinLiquidityQuote:       minLiquidity,
		MinLifetimeS:            minLifetime,
		BuyVenues:               parseVenueSet(q.Get("buy")),
		SellVenues:              parseVenueSet(q.Get("sell")),
		Quotes:                  parseQuoteSet(q.Get("quote")),
		Limit:                   limit,
		IncludeSuspect:          queryFlag(q.Get("include_suspect")),
		IncludeUnknownLiquidity: queryFlag(q.Get("include_unknown_liquidity")),
		MaxPlausibleSpreadBps:   snap.Settings.EffectiveMaxPlausibleSpreadBps(),
	}
	if base := q.Get("base"); base != "" {
		f.BasesAllow = map[string]bool{base: true}
	}

	res := screener.ComputeSpreads(s.Screener.Book, screenerSpotFeeLookup(snap), s.Screener.SpreadLifetime, nil, time.Now().UTC(), f)
	WriteData(w, http.StatusOK, map[string]any{
		"rows":         res.Rows,
		"total":        res.Total,
		"generated_at": time.Now().UTC(),
		"model":        "no-transfer, top-of-book",
		"excluded": map[string]int{
			"suspect":           res.ExcludedSuspect,
			"liquidity_unknown": res.ExcludedLiquidityUnknown,
		},
		"filters": map[string]any{
			"min_liquidity":             minLiquidity,
			"include_suspect":           f.IncludeSuspect,
			"include_unknown_liquidity": f.IncludeUnknownLiquidity,
			"max_plausible_spread_bps":  f.MaxPlausibleSpreadBps,
		},
	})
}

// parseQuoteSet splits ?quote=USDT,USDC into a set. Quote assets are
// matched exactly — USDT, USDC, FDUSD and USD are distinct quotes and
// are never merged (design §3).
func parseQuoteSet(csv string) map[string]bool {
	if csv == "" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(csv, ",") {
		part = strings.ToUpper(strings.TrimSpace(part))
		if part != "" {
			out[part] = true
		}
	}
	return out
}

func queryFlag(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func (s *Server) handleScreenerPerpetuals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	minCarry, ok := parseOptionalDecimal(w, r, "min_carry_apr")
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := screener.PerpFilters{
		Venue:       screener.Venue(q.Get("venue")),
		Base:        q.Get("base"),
		MinCarryAPR: minCarry,
		Limit:       limit,
	}
	snap, ok := s.screenerSnapshot(w, r)
	if !ok {
		return
	}
	rows := screener.ComputePerps(s.Screener.Book, screenerSpotMidLookup(s.Screener.Book),
		screenerSpotFeeLookup(snap), screenerPerpFeeLookup(snap), time.Now().UTC(), f)
	WriteData(w, http.StatusOK, map[string]any{"rows": rows, "generated_at": time.Now().UTC()})
}

// clampFundingHours bounds the funding-history window to the caller's
// entitlement retention (audit D9): hours past history.retention_days
// can only return rows the retention job is about to delete anyway, so
// an oversized request is trimmed rather than executed.
func clampFundingHours(hours int, p *Principal) int {
	if hours <= 0 {
		hours = 72
	}
	if p != nil {
		if max := 24 * p.Ent().History.RetentionDays; hours > max {
			hours = max
		}
	}
	return hours
}

func atoiOrZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func (s *Server) handleScreenerFunding(w http.ResponseWriter, r *http.Request) {
	if s.Screener == nil || s.Screener.Funding == nil {
		WriteData(w, http.StatusOK, map[string]any{"series": []screener.FundingSeries{}})
		return
	}
	q := r.URL.Query()
	p, _ := PrincipalFrom(r.Context())
	hours := clampFundingHours(atoiOrZero(q.Get("hours")), &p)
	venues := parseVenueSet(q.Get("venues"))
	venueList := make([]screener.Venue, 0, len(venues))
	for v := range venues {
		venueList = append(venueList, v)
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	series, err := s.Screener.Funding.ListFunding(r.Context(), q.Get("base"), venueList, since)
	if err != nil {
		s.log.Error("screener funding list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "funding_list_failed", "listing funding history failed", correlationID(r))
		return
	}
	WriteData(w, http.StatusOK, map[string]any{"series": series})
}

func (s *Server) handleScreenerCalculator(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Base             string         `json:"base"`
		Quote            string         `json:"quote"`
		BuyVenue         screener.Venue `json:"buy_venue"`
		SellVenue        screener.Venue `json:"sell_venue"`
		SizeQuote        string         `json:"size_quote"`
		TransferFeeQuote *string        `json:"transfer_fee_quote,omitempty"`
		OverrideFees     *struct {
			BuyFeeBps  *string `json:"buy_fee_bps,omitempty"`
			SellFeeBps *string `json:"sell_fee_bps,omitempty"`
		} `json:"override_fees,omitempty"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "invalid calculator payload: "+err.Error(), correlationID(r))
		return
	}
	sizeQuote, err := decimal.NewFromString(body.SizeQuote)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "size_quote must be a decimal", correlationID(r))
		return
	}
	req := screener.CalculatorRequest{
		Base: body.Base, Quote: body.Quote,
		BuyVenue: body.BuyVenue, SellVenue: body.SellVenue,
		SizeQuote: sizeQuote,
	}
	if body.TransferFeeQuote != nil {
		v, err := decimal.NewFromString(*body.TransferFeeQuote)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "bad_payload", "transfer_fee_quote must be a decimal", correlationID(r))
			return
		}
		req.TransferFeeQuote = &v
	}
	if body.OverrideFees != nil {
		if body.OverrideFees.BuyFeeBps != nil {
			v, err := decimal.NewFromString(*body.OverrideFees.BuyFeeBps)
			if err != nil {
				WriteError(w, http.StatusBadRequest, "bad_payload", "override_fees.buy_fee_bps must be a decimal", correlationID(r))
				return
			}
			req.OverrideBuyFeeBps = &v
		}
		if body.OverrideFees.SellFeeBps != nil {
			v, err := decimal.NewFromString(*body.OverrideFees.SellFeeBps)
			if err != nil {
				WriteError(w, http.StatusBadRequest, "bad_payload", "override_fees.sell_fee_bps must be a decimal", correlationID(r))
				return
			}
			req.OverrideSellFeeBps = &v
		}
	}

	snap, ok := s.screenerSnapshot(w, r)
	if !ok {
		return
	}
	res, err := screener.Calculate(s.Screener.Book, screenerSpotFeeLookup(snap), req)
	if err != nil {
		switch {
		case errors.Is(err, screener.ErrNoQuote):
			WriteError(w, http.StatusNotFound, "no_quote", "no current quote for the requested venue/pair", correlationID(r))
		case errors.Is(err, screener.ErrInvalid):
			WriteError(w, http.StatusBadRequest, "invalid_request", err.Error(), correlationID(r))
		default:
			s.log.Error("screener calculator failed", "error", err)
			WriteError(w, http.StatusInternalServerError, "calculator_failed", "calculator failed", correlationID(r))
		}
		return
	}
	WriteData(w, http.StatusOK, res)
}

func (s *Server) screenerSettingsView(snap screener.Snapshot) map[string]any {
	return map[string]any{
		"version":      snap.Version,
		"created_at":   snap.CreatedAt,
		"settings":     snap.Settings,
		"field_timing": screener.FieldTiming(snap.Settings),
	}
}

func (s *Server) handleScreenerSettingsGet(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.screenerSnapshot(w, r)
	if !ok {
		return
	}
	WriteData(w, http.StatusOK, s.screenerSettingsView(snap))
}

func (s *Server) handleScreenerSettingsApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Settings      screener.Settings `json:"settings"`
		ParentVersion *int64            `json:"parent_version,omitempty"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "invalid settings payload: "+err.Error(), correlationID(r))
		return
	}
	if body.ParentVersion == nil || *body.ParentVersion <= 0 {
		WriteError(w, http.StatusBadRequest, "parent_version_required", "parent_version is required", correlationID(r))
		return
	}
	principal, _ := PrincipalFrom(r.Context())
	// rules.min_refresh_s and venues.screener_max (packages.md §3.2).
	if s.writeEntitlementError(w, r, principal.Ent().CheckRefresh(body.Settings.PollIntervalS)) {
		return
	}
	enabled := make([]string, 0, len(body.Settings.Venues))
	for id, vs := range body.Settings.Venues {
		if vs.Enabled {
			enabled = append(enabled, string(id))
		}
	}
	if s.writeEntitlementError(w, r, principal.Ent().CheckVenues(enabled)) {
		return
	}
	if s.writeEntitlementError(w, r, principal.Ent().CheckVenueTiers(enabled, venueTier)) {
		return
	}
	snap, err := s.Screener.ApplyExpect(r.Context(), principal.UserID, "web", body.Settings, *body.ParentVersion)
	if err != nil {
		s.writeScreenerError(w, r, err)
		return
	}
	s.log.Info("screener settings change applied", "version", snap.Version, "actor", principal.UserID)
	WriteData(w, http.StatusOK, s.screenerSettingsView(snap))
}

func (s *Server) handleScreenerRulesList(w http.ResponseWriter, r *http.Request) {
	rules, err := s.Screener.Rules.ListRules(r.Context())
	if err != nil {
		s.log.Error("screener rules list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "rules_list_failed", "listing rules failed", correlationID(r))
		return
	}
	out := make([]screener.Rule, len(rules))
	for i, rl := range rules {
		out[i] = rl.Redact()
	}
	WriteData(w, http.StatusOK, map[string]any{"rules": out})
}

func (s *Server) decodeScreenerRule(w http.ResponseWriter, r *http.Request) (screener.Rule, bool) {
	var rule screener.Rule
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rule); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "invalid rule payload: "+err.Error(), correlationID(r))
		return screener.Rule{}, false
	}
	return rule, true
}

func (s *Server) handleScreenerRuleCreate(w http.ResponseWriter, r *http.Request) {
	rule, ok := s.decodeScreenerRule(w, r)
	if !ok {
		return
	}
	rule.ID = newScreenerID("rule")
	if err := rule.Validate(); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_rule", err.Error(), correlationID(r))
		return
	}
	principal, _ := PrincipalFrom(r.Context())
	existing, err := s.Screener.Rules.ListRules(r.Context())
	if err != nil {
		s.log.Error("screener rules list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "rules_list_failed", "listing rules failed", correlationID(r))
		return
	}
	if s.writeEntitlementError(w, r, principal.Ent().CheckRuleCount(len(existing))) || !s.enforceRule(w, r, principal.Ent(), rule) {
		return
	}
	created, err := s.Screener.Rules.InsertRule(r.Context(), rule, principal.UserID)
	if err != nil {
		s.log.Error("screener rule create failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "rule_create_failed", "creating the rule failed", correlationID(r))
		return
	}
	s.audit(r, principal.UserID, "screener.rule.create", "screener_rule:"+created.ID)
	WriteData(w, http.StatusCreated, map[string]any{"rule": created.Redact()})
}

func (s *Server) handleScreenerRuleUpdate(w http.ResponseWriter, r *http.Request) {
	rule, ok := s.decodeScreenerRule(w, r)
	if !ok {
		return
	}
	rule.ID = r.PathValue("id")
	// webhook_secret is write-only (never returned by GET/list, so the
	// console cannot echo it back): an empty value on update means
	// "keep the existing secret", not "clear it". A caller that wants
	// to rotate the secret sends a new non-empty value.
	if rule.WebhookSecret == "" {
		if existing, err := s.Screener.Rules.GetRule(r.Context(), rule.ID); err == nil {
			rule.WebhookSecret = existing.WebhookSecret
		}
	}
	if err := rule.Validate(); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_rule", err.Error(), correlationID(r))
		return
	}
	principal, _ := PrincipalFrom(r.Context())
	if !s.enforceRule(w, r, principal.Ent(), rule) {
		return
	}
	updated, err := s.Screener.Rules.UpdateRule(r.Context(), rule, principal.UserID)
	if err != nil {
		s.writeScreenerError(w, r, err)
		return
	}
	s.audit(r, principal.UserID, "screener.rule.update", "screener_rule:"+rule.ID)
	WriteData(w, http.StatusOK, map[string]any{"rule": updated.Redact()})
}

// venueTier is the tier lookup injected into
// entitlements.CheckVenueTiers (T-104). It lives here, in the layer that
// already imports both packages, so internal/entitlements keeps no
// dependency on internal/screener. An id the screener does not know
// returns "", which CheckVenueTiers refuses.
func venueTier(id string) string { return screener.VenueTiers[screener.Venue(id)] }

// enforceRule applies the packages.md §3.2 rule-level entitlements:
// kind, venues (fixed set / count and tier), cooldown floor, every
// requested alert channel, and — when the rule asks for automatic paper
// execution — the strategy list and the decimal size cap. Writes the
// 403 itself.
func (s *Server) enforceRule(w http.ResponseWriter, r *http.Request, ent entitlements.Entitlements, rule screener.Rule) bool {
	venues := make([]string, 0, len(rule.BuyVenues)+len(rule.SellVenues))
	for _, v := range rule.BuyVenues {
		venues = append(venues, string(v))
	}
	for _, v := range rule.SellVenues {
		venues = append(venues, string(v))
	}
	checks := []error{
		ent.CheckRuleKind(string(rule.Kind)),
		ent.CheckVenues(venues),
		ent.CheckVenueTiers(venues, venueTier),
		ent.CheckCooldown(rule.CooldownS),
	}
	for _, ch := range rule.EffectiveChannels() {
		checks = append(checks, ent.CheckChannel(ch))
	}
	if rule.AutoPaper {
		checks = append(checks, ent.CheckAutoPaper(string(rule.EffectiveStrategy()), rule.PaperSizeQuote))
	}
	for _, err := range checks {
		if s.writeEntitlementError(w, r, err) {
			return false
		}
	}
	return true
}

func (s *Server) handleScreenerRuleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Screener.Rules.DeleteRule(r.Context(), id); err != nil {
		s.writeScreenerError(w, r, err)
		return
	}
	principal, _ := PrincipalFrom(r.Context())
	s.audit(r, principal.UserID, "screener.rule.delete", "screener_rule:"+id)
	WriteData(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func (s *Server) handleScreenerEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	events, err := s.Screener.Events.ListEvents(r.Context(), q.Get("rule_id"), limit)
	if err != nil {
		s.log.Error("screener events list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "events_list_failed", "listing events failed", correlationID(r))
		return
	}
	// history.retention_days: rows older than the package depth are
	// invisible through the API (packages.md §3.2); they are not
	// deleted here (30-day grace on downgrade belongs to the retention
	// job).
	principal, _ := PrincipalFrom(r.Context())
	cutoff := principal.Ent().RetentionCutoff(time.Now().UTC())
	visible := make([]screener.Event, 0, len(events))
	for _, e := range events {
		if !e.OpenedAt.Before(cutoff) {
			visible = append(visible, e)
		}
	}
	WriteData(w, http.StatusOK, map[string]any{"events": visible, "retention_days": principal.Ent().History.RetentionDays})
}

// handleScreenerAutoPaper serves the T-071 executor's read model
// (design §7; statistics per strategy-models §7). When no executor runs
// in this profile the document is empty and says so ("executor":
// "not_running") — never fabricated data. Read-only: screener:view.
func (s *Server) handleScreenerAutoPaper(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	src := s.Screener.AutoPaper()
	if src == nil {
		WriteData(w, http.StatusOK, map[string]any{
			"positions":    []any{},
			"summary":      map[string]any{"per_rule": []any{}},
			"balances":     []any{},
			"generated_at": now,
			"executor":     "not_running",
			"model":        "paper only; no executor in this profile",
		})
		return
	}
	view, err := src.AutoPaperView(r.Context(), now)
	if err != nil {
		s.log.Error("screener auto-paper view failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "auto_paper_failed", "reading the auto-paper ledger failed", correlationID(r))
		return
	}
	WriteData(w, http.StatusOK, map[string]any{
		"positions":    view.Positions,
		"summary":      view.Summary,
		"balances":     view.Balances,
		"generated_at": view.GeneratedAt,
		"executor":     "running",
		"model":        view.Model,
	})
}

func (s *Server) handleScreenerTemplatesList(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFrom(r.Context())
	templates, err := s.Screener.Templates.ListTemplates(r.Context(), principal.UserID)
	if err != nil {
		s.log.Error("screener templates list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "templates_list_failed", "listing templates failed", correlationID(r))
		return
	}
	WriteData(w, http.StatusOK, map[string]any{"templates": templates})
}

func (s *Server) handleScreenerTemplateCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string          `json:"name"`
		Filters json.RawMessage `json:"filters"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "invalid template payload: "+err.Error(), correlationID(r))
		return
	}
	if body.Name == "" {
		WriteError(w, http.StatusBadRequest, "invalid_template", "name is required", correlationID(r))
		return
	}
	principal, _ := PrincipalFrom(r.Context())
	existing, err := s.Screener.Templates.ListTemplates(r.Context(), principal.UserID)
	if err != nil {
		s.log.Error("screener templates list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "templates_list_failed", "listing templates failed", correlationID(r))
		return
	}
	if s.writeEntitlementError(w, r, principal.Ent().CheckTemplateCount(len(existing))) {
		return
	}
	tpl := screener.Template{ID: newScreenerID("tpl"), UserID: principal.UserID, Name: body.Name, Filters: body.Filters}
	created, err := s.Screener.Templates.InsertTemplate(r.Context(), tpl)
	if err != nil {
		s.log.Error("screener template create failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "template_create_failed", "creating the template failed", correlationID(r))
		return
	}
	s.audit(r, principal.UserID, "screener.template.create", "screener_template:"+created.ID)
	WriteData(w, http.StatusCreated, map[string]any{"template": created})
}

func (s *Server) handleScreenerTemplateDelete(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFrom(r.Context())
	id := r.PathValue("id")
	if err := s.Screener.Templates.DeleteTemplate(r.Context(), principal.UserID, id); err != nil {
		s.writeScreenerError(w, r, err)
		return
	}
	s.audit(r, principal.UserID, "screener.template.delete", "screener_template:"+id)
	WriteData(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func (s *Server) writeScreenerError(w http.ResponseWriter, r *http.Request, err error) {
	var stale *screener.StaleVersionError
	switch {
	case errors.As(err, &stale):
		WriteErrorData(w, http.StatusConflict, "stale_version",
			"settings changed since you loaded them; reload and retry", correlationID(r),
			map[string]any{"current_version": stale.Current})
	case errors.Is(err, screener.ErrNoChange):
		WriteError(w, http.StatusBadRequest, "no_change", "payload equals the current version", correlationID(r))
	case errors.Is(err, screener.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found", "no such screener resource", correlationID(r))
	case errors.Is(err, screener.ErrInvalid):
		WriteError(w, http.StatusBadRequest, "invalid_settings", err.Error(), correlationID(r))
	default:
		s.log.Error("screener change failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "screener_failed", "screener change failed", correlationID(r))
	}
}
