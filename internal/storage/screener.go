package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// Organisation scoping (T-081, migration 000013). Every screener table
// carries org_id. Queries read the organisation from the context
// (tenancy.WithOrg, set by the API middleware on every authenticated
// request): with a scope, rows of other organisations are invisible;
// without one (engine loop, background jobs, tests that predate
// tenancy) the query sees every organisation, and inserts land in the
// platform organisation. orgFilter renders the WHERE fragment for a
// given argument index so the two shapes share one query string.
func orgFilter(ctx context.Context, col string, argIdx int) (string, []any) {
	if id, ok := tenancy.OrgFrom(ctx); ok {
		return fmt.Sprintf(" AND %s = $%d", col, argIdx), []any{id}
	}
	return "", nil
}

// ScreenerSettings adapts the store to screener.SettingsStore: immutable
// version rows in screener_settings with exactly one active per
// organisation (migration 000017; same shape as PlatformSettings,
// internal/storage/platformsettings.go, plus the org_id every other
// screener table carries). The organisation comes from ctx: Insert
// deactivates and writes within it, Active reads it, Get/List filter on
// it like the sibling stores; unscoped means the platform organisation.
type ScreenerSettings struct{ s *Store }

func (s *Store) ScreenerSettings() *ScreenerSettings { return &ScreenerSettings{s: s} }

func (c *ScreenerSettings) Insert(ctx context.Context, createdBy string, payload, diff json.RawMessage, parent int64) (int64, time.Time, error) {
	org := tenancy.OrgOrPlatform(ctx)
	tx, err := c.s.Pool.Begin(ctx)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE screener_settings SET active = FALSE WHERE active AND org_id = $1`, org); err != nil {
		return 0, time.Time{}, err
	}
	var (
		version   int64
		createdAt time.Time
	)
	err = tx.QueryRow(ctx, `
		INSERT INTO screener_settings (created_by, active, payload, diff, parent_version, org_id)
		VALUES (NULLIF($1,''), TRUE, $2, $3, NULLIF($4,0), $5)
		RETURNING version, created_at`,
		createdBy, payload, diff, parent, org).Scan(&version, &createdAt)
	if err != nil {
		return 0, time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, time.Time{}, err
	}
	return version, createdAt, nil
}

func (c *ScreenerSettings) Active(ctx context.Context) (screener.Snapshot, bool, error) {
	snap, err := c.scanOne(ctx, `WHERE active AND org_id = $1`, tenancy.OrgOrPlatform(ctx))
	if errors.Is(err, pgx.ErrNoRows) {
		return screener.Snapshot{}, false, nil
	}
	if err != nil {
		return screener.Snapshot{}, false, err
	}
	return snap, true, nil
}

func (c *ScreenerSettings) Get(ctx context.Context, version int64) (screener.Snapshot, error) {
	where, args := orgFilter(ctx, "org_id", 2)
	snap, err := c.scanOne(ctx, `WHERE version = $1`+where, append([]any{version}, args...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return screener.Snapshot{}, screener.ErrNotFound
	}
	return snap, err
}

func (c *ScreenerSettings) scanOne(ctx context.Context, where string, args ...any) (screener.Snapshot, error) {
	var (
		snap      screener.Snapshot
		payload   []byte
		createdBy *string
		parent    *int64
	)
	err := c.s.Pool.QueryRow(ctx, `
		SELECT version, payload, created_by, created_at, parent_version
		FROM screener_settings `+where, args...).
		Scan(&snap.Version, &payload, &createdBy, &snap.CreatedAt, &parent)
	if err != nil {
		return screener.Snapshot{}, err
	}
	if err := json.Unmarshal(payload, &snap.Settings); err != nil {
		return screener.Snapshot{}, err
	}
	if createdBy != nil {
		snap.CreatedBy = *createdBy
	}
	if parent != nil {
		snap.ParentVer = *parent
	}
	return snap, nil
}

func (c *ScreenerSettings) List(ctx context.Context, limit int) ([]screener.VersionInfo, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where, args := orgFilter(ctx, "org_id", 2)
	rows, err := c.s.Pool.Query(ctx, `
		SELECT version, created_by, created_at, active, diff, parent_version
		FROM screener_settings WHERE TRUE`+where+` ORDER BY version DESC LIMIT $1`, append([]any{limit}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []screener.VersionInfo
	for rows.Next() {
		var (
			v         screener.VersionInfo
			createdBy *string
			diff      []byte
			parent    *int64
		)
		if err := rows.Scan(&v.Version, &createdBy, &v.CreatedAt, &v.Active, &diff, &parent); err != nil {
			return nil, err
		}
		if createdBy != nil {
			v.CreatedBy = *createdBy
		}
		if parent != nil {
			v.ParentVer = *parent
		}
		v.Diff = diff
		out = append(out, v)
	}
	return out, rows.Err()
}

// ScreenerRules adapts the store to screener.RuleStore: one mutable row
// per rule, the full Rule serialized as payload.
type ScreenerRules struct{ s *Store }

func (s *Store) ScreenerRules() *ScreenerRules { return &ScreenerRules{s: s} }

func (c *ScreenerRules) ListRules(ctx context.Context) ([]screener.Rule, error) {
	where, args := orgFilter(ctx, "org_id", 1)
	rows, err := c.s.Pool.Query(ctx, `SELECT payload FROM screener_rules WHERE TRUE`+where+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []screener.Rule
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var r screener.Rule
		if err := json.Unmarshal(payload, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (c *ScreenerRules) GetRule(ctx context.Context, id string) (screener.Rule, error) {
	var payload []byte
	where, args := orgFilter(ctx, "org_id", 2)
	err := c.s.Pool.QueryRow(ctx, `SELECT payload FROM screener_rules WHERE id = $1`+where, append([]any{id}, args...)...).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return screener.Rule{}, screener.ErrNotFound
	}
	if err != nil {
		return screener.Rule{}, err
	}
	var r screener.Rule
	if err := json.Unmarshal(payload, &r); err != nil {
		return screener.Rule{}, err
	}
	return r, nil
}

func (c *ScreenerRules) InsertRule(ctx context.Context, r screener.Rule, actor string) (screener.Rule, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return screener.Rule{}, err
	}
	if _, err := c.s.Pool.Exec(ctx, `
		INSERT INTO screener_rules (id, payload, enabled, updated_by, org_id)
		VALUES ($1, $2, $3, NULLIF($4,''), $5)`, r.ID, payload, r.Enabled, actor, tenancy.OrgOrPlatform(ctx)); err != nil {
		return screener.Rule{}, err
	}
	return r, nil
}

func (c *ScreenerRules) UpdateRule(ctx context.Context, r screener.Rule, actor string) (screener.Rule, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return screener.Rule{}, err
	}
	where, args := orgFilter(ctx, "org_id", 5)
	tag, err := c.s.Pool.Exec(ctx, `
		UPDATE screener_rules SET payload = $2, enabled = $3, updated_at = now(), updated_by = NULLIF($4,'')
		WHERE id = $1`+where, append([]any{r.ID, payload, r.Enabled, actor}, args...)...)
	if err != nil {
		return screener.Rule{}, err
	}
	if tag.RowsAffected() == 0 {
		return screener.Rule{}, screener.ErrNotFound
	}
	return r, nil
}

func (c *ScreenerRules) DeleteRule(ctx context.Context, id string) error {
	where, args := orgFilter(ctx, "org_id", 2)
	tag, err := c.s.Pool.Exec(ctx, `DELETE FROM screener_rules WHERE id = $1`+where, append([]any{id}, args...)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return screener.ErrNotFound
	}
	return nil
}

// ScreenerEvents adapts the store to screener.EventStore.
type ScreenerEvents struct{ s *Store }

func (s *Store) ScreenerEvents() *ScreenerEvents { return &ScreenerEvents{s: s} }

func (c *ScreenerEvents) ListEvents(ctx context.Context, ruleID string, limit int) ([]screener.Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where, args := orgFilter(ctx, "org_id", 3)
	rows, err := c.s.Pool.Query(ctx, `
		SELECT id, rule_id, kind, base, quote, buy_venue, sell_venue,
		       opened_at, closed_at, lifetime_s, peak_net_bps,
		       telegram_sent, paper_execution_id, delivered, close_reason
		FROM screener_events WHERE ($1 = '' OR rule_id = $1)`+where+` ORDER BY opened_at DESC LIMIT $2`,
		append([]any{ruleID, limit}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []screener.Event
	for rows.Next() {
		var (
			e                   screener.Event
			buyVenue, sellVenue *string
			peakNetBps          string
			paperExecID         *string
			delivered           []byte
			closeReason         *string
		)
		if err := rows.Scan(&e.ID, &e.RuleID, &e.Kind, &e.Base, &e.Quote, &buyVenue, &sellVenue,
			&e.OpenedAt, &e.ClosedAt, &e.LifetimeS, &peakNetBps, &e.TelegramSent, &paperExecID, &delivered, &closeReason); err != nil {
			return nil, err
		}
		if closeReason != nil {
			e.CloseReason = *closeReason
		}
		if buyVenue != nil {
			e.BuyVenue = screener.Venue(*buyVenue)
		}
		if sellVenue != nil {
			e.SellVenue = screener.Venue(*sellVenue)
		}
		e.PeakNetBps = peakNetBps
		e.PaperExecutionID = paperExecID
		if len(delivered) > 0 {
			if err := json.Unmarshal(delivered, &e.Delivered); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (c *ScreenerEvents) InsertEvent(ctx context.Context, e screener.Event) error {
	var buyVenue, sellVenue *string
	if e.BuyVenue != "" {
		v := string(e.BuyVenue)
		buyVenue = &v
	}
	if e.SellVenue != "" {
		v := string(e.SellVenue)
		sellVenue = &v
	}
	deliveredMap := e.Delivered
	if deliveredMap == nil {
		deliveredMap = map[string]screener.DeliveryOutcome{}
	}
	delivered, err := json.Marshal(deliveredMap)
	if err != nil {
		return err
	}
	// The event inherits its rule's organisation (the evaluator runs
	// without a request scope); an unknown rule falls back to the
	// platform organisation.
	_, err = c.s.Pool.Exec(ctx, `
		INSERT INTO screener_events
			(id, rule_id, kind, base, quote, buy_venue, sell_venue,
			 opened_at, closed_at, lifetime_s, peak_net_bps, telegram_sent, paper_execution_id, org_id, delivered)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,
		        COALESCE((SELECT org_id FROM screener_rules WHERE id = $2), $14), $15)
		ON CONFLICT (id) DO NOTHING`,
		e.ID, e.RuleID, string(e.Kind), e.Base, e.Quote, buyVenue, sellVenue,
		e.OpenedAt, e.ClosedAt, e.LifetimeS, e.PeakNetBps, e.TelegramSent, e.PaperExecutionID, tenancy.OrgOrPlatform(ctx), delivered)
	return err
}

var _ screener.EventDeliveryRecorder = (*ScreenerEvents)(nil)

// SetEventDelivered patches one channel's delivery outcome onto an
// already-inserted event (T-086): email/webhook results are only known
// after InsertEvent has already run, since they are dispatched to a
// bounded worker rather than awaited inline.
func (c *ScreenerEvents) SetEventDelivered(ctx context.Context, id, channel string, outcome screener.DeliveryOutcome) error {
	out, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	tag, err := c.s.Pool.Exec(ctx, `
		UPDATE screener_events SET delivered = jsonb_set(COALESCE(delivered, '{}'::jsonb), ARRAY[$2], $3::jsonb, true)
		WHERE id = $1`, id, channel, out)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return screener.ErrNotFound
	}
	return nil
}

// ScreenerTemplates adapts the store to screener.TemplateStore.
type ScreenerTemplates struct{ s *Store }

func (s *Store) ScreenerTemplates() *ScreenerTemplates { return &ScreenerTemplates{s: s} }

func (c *ScreenerTemplates) ListTemplates(ctx context.Context, userID string) ([]screener.Template, error) {
	where, args := orgFilter(ctx, "org_id", 2)
	rows, err := c.s.Pool.Query(ctx, `
		SELECT id, user_id, name, filters, created_at
		FROM screener_templates WHERE user_id = $1`+where+` ORDER BY created_at DESC`, append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []screener.Template
	for rows.Next() {
		var t screener.Template
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Filters, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (c *ScreenerTemplates) InsertTemplate(ctx context.Context, t screener.Template) (screener.Template, error) {
	err := c.s.Pool.QueryRow(ctx, `
		INSERT INTO screener_templates (id, user_id, name, filters, org_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`, t.ID, t.UserID, t.Name, t.Filters, tenancy.OrgOrPlatform(ctx)).Scan(&t.CreatedAt)
	if err != nil {
		return screener.Template{}, err
	}
	return t, nil
}

func (c *ScreenerTemplates) DeleteTemplate(ctx context.Context, userID, id string) error {
	where, args := orgFilter(ctx, "org_id", 3)
	tag, err := c.s.Pool.Exec(ctx, `DELETE FROM screener_templates WHERE id = $1 AND user_id = $2`+where, append([]any{id, userID}, args...)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return screener.ErrNotFound
	}
	return nil
}

// ScreenerFunding adapts the store to screener.FundingStore.
type ScreenerFunding struct{ s *Store }

func (s *Store) ScreenerFunding() *ScreenerFunding { return &ScreenerFunding{s: s} }

func (c *ScreenerFunding) UpsertFunding(ctx context.Context, venue screener.Venue, base string, at time.Time, rate string) error {
	_, err := c.s.Pool.Exec(ctx, `
		INSERT INTO funding_history (venue, base, at, rate)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (venue, base, at) DO NOTHING`, string(venue), base, at, rate)
	return err
}

// fundingRowLimit bounds every ListFunding branch (audit D9): the query
// shares the API/outbox pool, so an unbounded scan of funding_history
// could pin connections the hot path needs. The bound sits far above
// any window the console requests (the default 72 h across every venue
// and base is thousands of rows); the primary key's (venue, base, at)
// order lets the index stop at the limit instead of sorting.
const fundingRowLimit = 50000

func (c *ScreenerFunding) ListFunding(ctx context.Context, base string, venues []screener.Venue, since time.Time) ([]screener.FundingSeries, error) {
	venueStrs := make([]string, len(venues))
	for i, v := range venues {
		venueStrs[i] = string(v)
	}
	var rows pgxRows
	var err error
	switch {
	case base != "" && len(venueStrs) > 0:
		rows, err = c.s.Pool.Query(ctx, `
			SELECT venue, base, at, rate FROM funding_history
			WHERE base = $1 AND venue = ANY($2) AND at >= $3 ORDER BY venue, base, at LIMIT $4`, base, venueStrs, since, fundingRowLimit)
	case base != "":
		rows, err = c.s.Pool.Query(ctx, `
			SELECT venue, base, at, rate FROM funding_history
			WHERE base = $1 AND at >= $2 ORDER BY venue, base, at LIMIT $3`, base, since, fundingRowLimit)
	case len(venueStrs) > 0:
		rows, err = c.s.Pool.Query(ctx, `
			SELECT venue, base, at, rate FROM funding_history
			WHERE venue = ANY($1) AND at >= $2 ORDER BY venue, base, at LIMIT $3`, venueStrs, since, fundingRowLimit)
	default:
		rows, err = c.s.Pool.Query(ctx, `
			SELECT venue, base, at, rate FROM funding_history
			WHERE at >= $1 ORDER BY venue, base, at LIMIT $2`, since, fundingRowLimit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bySeries := map[[2]string]*screener.FundingSeries{}
	var order [][2]string
	for rows.Next() {
		var venue, b, rate string
		var at time.Time
		if err := rows.Scan(&venue, &b, &at, &rate); err != nil {
			return nil, err
		}
		key := [2]string{venue, b}
		series, ok := bySeries[key]
		if !ok {
			series = &screener.FundingSeries{Venue: screener.Venue(venue), Base: b}
			bySeries[key] = series
			order = append(order, key)
		}
		series.Points = append(series.Points, screener.FundingPoint{At: at, Rate: rate})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]screener.FundingSeries, 0, len(order))
	for _, k := range order {
		out = append(out, *bySeries[k])
	}
	return out, nil
}

// pgxRows narrows pgx.Rows to what this file needs, so the three query
// branches in ListFunding/ListEvents can share one scanning loop without
// importing pgx.Rows' full interface signature repeatedly.
type pgxRows = pgx.Rows
