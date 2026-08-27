package storage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

// Integration tests run against a real PostgreSQL with the migrations
// applied. Set ARB_TEST_DATABASE_URL to enable; without it the package
// skips (CI adds a service container when runners are available).
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("ARB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ARB_TEST_DATABASE_URL not set; skipping storage integration tests")
	}
	s, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	// organisations.referred_by → affiliate_accounts has no cascade and
	// organisations is never truncated (the platform row must survive):
	// clear the link first or a referral left by a previous run blocks
	// the affiliate_accounts delete below.
	if _, err := s.Pool.Exec(context.Background(), "UPDATE organisations SET referred_by = NULL WHERE referred_by IS NOT NULL"); err != nil {
		t.Fatalf("clear organisations.referred_by: %v", err)
	}
	for _, table := range []string{"fills", "orders", "paper_cycles", "paper_sessions",
		"opportunities", "triangles", "markets", "sessions",
		"strategy_configs", "platform_settings",
		// screener_settings/_rules FK-reference users(id) with no cascade
		// (same as platform_settings above): deleted before "users" below.
		"screener_reports", "screener_paper_executions", "screener_paper_positions", "screener_paper_balances",
		"screener_templates", "funding_history", "screener_events", "screener_rules", "screener_settings",
		"audit_events", "ai_recommendations", "ai_analyses",
		"affiliate_ledger", "affiliate_accounts", "paddle_events", "subscriptions", "billing_prices",
		"memberships", "alerts", "secrets", "users", "exchanges", "campaign_runs", "replay_runs", "risk_events", "reports"} {
		if _, err := s.Pool.Exec(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	return s
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var t0 = time.Unix(1_700_000_000, 0).UTC()

func TestAuthStoreRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	as := s.Auth()

	hash, _ := auth.HashPassword("pw")
	u := auth.User{ID: "u1", Email: "a@example.test", PasswordHash: hash, Role: auth.RoleOperator}
	if err := as.UpsertUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, err := as.UserByEmail(ctx, "a@example.test")
	if err != nil || got.Role != auth.RoleOperator || got.Disabled {
		t.Fatalf("user = %+v err=%v", got, err)
	}
	if _, err := as.UserByEmail(ctx, "ghost@example.test"); !errors.Is(err, auth.ErrUnknownUser) {
		t.Fatalf("ghost = %v", err)
	}

	sess := auth.Session{Token: "tok-1", UserID: "u1", CreatedAt: t0, ExpiresAt: t0.Add(time.Hour)}
	if err := as.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	back, err := as.SessionByToken(ctx, "tok-1")
	if err != nil || back.UserID != "u1" || back.Role != auth.RoleOperator || !back.RevokedAt.IsZero() {
		t.Fatalf("session = %+v err=%v", back, err)
	}
	if err := as.RevokeSession(ctx, "tok-1", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	back, _ = as.SessionByToken(ctx, "tok-1")
	if back.RevokedAt.IsZero() {
		t.Fatal("revocation not persisted")
	}
	// Exactly-once revoke; unknown token errors.
	if err := as.RevokeSession(ctx, "tok-1", t0); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("double revoke = %v", err)
	}
	// Manager works end-to-end over the pgx stores.
	m := &auth.Manager{Users: as, Sessions: as, TTL: time.Hour, Now: time.Now}
	live, err := m.Login(ctx, "a@example.test", "pw", clientIP())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Validate(ctx, live.Token); err != nil {
		t.Fatal(err)
	}
}

func TestOpportunityAndCyclePersistence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	q := pricing.CycleQuote{
		Triangle: "binance|USDT|BTCUSDT>ETHBTC>ETHUSDT", Start: "USDT",
		InputConsumed: d("1000"), FinalAmount: d("1016.94204"),
	}
	q.GrossProfit = q.FinalAmount.Sub(q.InputConsumed)
	q.ReturnBps = d("169.4204")
	op := opportunity.Build("op-1", "binance", q,
		opportunity.Buffers{LatencyBps: d("2"), RiskBps: d("3")}, time.Second, t0, 1)
	dec := risk.Decision{Allowed: true, Checks: []risk.Check{{Name: "RISK_MIN_EDGE", Passed: true}}}

	if err := s.InsertOpportunity(ctx, &op, &dec); err != nil {
		t.Fatal(err)
	}
	// Idempotent replay.
	if err := s.InsertOpportunity(ctx, &op, &dec); err != nil {
		t.Fatal(err)
	}
	var count int
	var netProfit decimal.Decimal
	row := s.Pool.QueryRow(ctx, `SELECT count(*), min(net_profit) FROM opportunities`)
	if err := row.Scan(&count, &netProfit); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !netProfit.Equal(op.NetProfit) {
		t.Fatalf("opportunities = %d, net %s (want %s)", count, netProfit, op.NetProfit)
	}

	if err := s.EnsurePaperSession(ctx, "sess-1", "PAPER",
		map[string]string{"USDT": "10000"}, 1, 42); err != nil {
		t.Fatal(err)
	}
	mkt := exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}
	res := execution.CycleResult{
		CycleID: "cyc-1", OpportunityID: "op-1",
		Outcome: execution.OutcomeAllFilled, StartAsset: "USDT",
		InputConsumed: d("1000"), FinalAmount: d("1016.94204"),
		RealizedPnL: d("16.94204"), TotalPnL: d("16.94204"),
		Exposure: map[exchange.Asset]decimal.Decimal{"ETH": d("0.0001")},
		Fees:     map[exchange.Asset]decimal.Decimal{"BTC": d("0.01")},
		Orders: []execution.SimOrder{{
			ID: "ord-1", LegNo: 1, Market: mkt, Side: exchange.SideBuy, Type: "LIMIT_IOC",
			QtyRequested: d("10"), QtyFilled: d("10"), LimitPrice: d("100.2"),
			AvgPrice: d("100"), FeeAmount: d("0.01"), FeeAsset: "BTC",
			CreatedAt: t0, AckedAt: t0.Add(20 * time.Millisecond), FilledAt: t0.Add(50 * time.Millisecond),
			Status: execution.OrderFilled,
			Fills: []execution.SimFill{{
				ID: "fill-1", Price: d("100"), Qty: d("10"),
				FeeAmount: d("0.01"), FeeAsset: "BTC", BookVersion: 7, At: t0.Add(50 * time.Millisecond),
			}},
		}},
		StartedAt: t0, SettledAt: t0.Add(200 * time.Millisecond),
	}
	if err := s.InsertCycle(ctx, "sess-1", &res); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCycle(ctx, "sess-1", &res); err != nil { // idempotent
		t.Fatal(err)
	}
	// Correlation chain fill → order → cycle → opportunity intact.
	row = s.Pool.QueryRow(ctx, `
		SELECT count(*) FROM fills f
		JOIN orders o ON o.id = f.order_id
		JOIN paper_cycles c ON c.id = o.cycle_id
		JOIN opportunities op ON op.id = c.opportunity_id
		WHERE op.id = 'op-1'`)
	if err := row.Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("correlation chain rows = %d", count)
	}
	// NUMERIC round-trip preserves the exact decimal.
	var pnl decimal.Decimal
	if err := s.Pool.QueryRow(ctx, `SELECT pnl_amount FROM paper_cycles WHERE id='cyc-1'`).Scan(&pnl); err != nil {
		t.Fatal(err)
	}
	if !pnl.Equal(d("16.94204")) {
		t.Fatalf("pnl round-trip = %s", pnl)
	}

	// Read-side list queries (T-024 route groups) over the same rows.
	opps, err := s.ListOpportunities(ctx, "", 10)
	if err != nil || len(opps) != 1 {
		t.Fatalf("ListOpportunities = %+v err=%v", opps, err)
	}
	if opps[0].ID != "op-1" || opps[0].StartAmount != "1000" || *opps[0].NetReturnBps == "" {
		t.Fatalf("opportunity row = %+v", opps[0])
	}
	if none, err := s.ListOpportunities(ctx, "REJECTED", 10); err != nil || len(none) != 0 {
		t.Fatalf("status filter leaked: %+v err=%v", none, err)
	}
	cycles, err := s.ListCycles(ctx, "sess-1", 10)
	if err != nil || len(cycles) != 1 || cycles[0].Outcome != "ALL_FILLED" || *cycles[0].PnLAmount != "16.94204" {
		t.Fatalf("ListCycles = %+v err=%v", cycles, err)
	}
	orders, err := s.ListOrders(ctx, "cyc-1")
	if err != nil || len(orders) != 1 || orders[0].Side != "BUY" || *orders[0].AvgPrice != "100" {
		t.Fatalf("ListOrders = %+v err=%v", orders, err)
	}
	if err := s.InsertAuditEvent(ctx, AuditRow{
		ID: "aud-1", Actor: "telegram:1", Source: "telegram",
		Action: "paper_pause", Entity: "paper_engine",
	}); err != nil {
		t.Fatal(err)
	}
	audits, err := s.ListAuditEvents(ctx, "paper_engine", 10)
	if err != nil || len(audits) != 1 || audits[0].Action != "paper_pause" {
		t.Fatalf("ListAuditEvents = %+v err=%v", audits, err)
	}
}

func TestUpsertMarketsAndOutbox(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	step := d("0.001")
	markets := []exchange.Market{{
		ID:   exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"},
		Base: "BTC", Quote: "USDT", Enabled: true, Status: exchange.MarketTrading,
		Rules: exchange.InstrumentRules{
			QtyMode: exchange.PrecisionStep, QtyStep: step,
			PriceMode: exchange.PrecisionStep, PriceTick: step,
			MinNotional: d("5"),
		},
	}}
	if err := s.UpsertMarkets(ctx, markets); err != nil {
		t.Fatal(err)
	}
	markets[0].Status = exchange.MarketHalted
	if err := s.UpsertMarkets(ctx, markets); err != nil { // update path
		t.Fatal(err)
	}
	var status string
	if err := s.Pool.QueryRow(ctx, `SELECT status FROM markets WHERE id='binance:BTCUSDT'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "HALTED" {
		t.Fatalf("status = %s", status)
	}

	// Outbox end-to-end: enqueue → run → persisted.
	q := pricing.CycleQuote{Triangle: "binance|USDT|A>B>C", Start: "USDT", InputConsumed: d("10")}
	op := opportunity.Build("op-ob", "binance", q, opportunity.Buffers{}, time.Second, t0, 1)
	ob := &Outbox{Store: s, Log: slog.New(slog.NewTextHandler(os.Stderr, nil)), SessionID: "sess-ob", FlushInterval: 10 * time.Millisecond}
	if !ob.Enqueue(Record{Kind: "opportunity", Opportunity: &op, Decision: &risk.Decision{}}) {
		t.Fatal("enqueue refused")
	}
	// Cycle records carry their opportunity linkage inside the result
	// (audit CR-P1-3) — no caller-side re-attachment.
	if err := s.EnsurePaperSession(ctx, "sess-ob", "PAPER", map[string]string{"USDT": "100"}, 1, 7); err != nil {
		t.Fatal(err)
	}
	cyc := execution.CycleResult{
		CycleID: "cyc-ob", OpportunityID: "op-ob",
		Outcome: execution.OutcomeAllFilled, StartAsset: "USDT",
		StartedAt: t0, SettledAt: t0.Add(time.Second),
	}
	if !ob.Enqueue(Record{Kind: "cycle", Cycle: &cyc, SessionID: "sess-ob"}) {
		t.Fatal("enqueue refused")
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- ob.Run(runCtx) }()
	deadline := time.After(3 * time.Second)
	for ob.Written() < 2 {
		select {
		case <-deadline:
			t.Fatal("outbox never wrote")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM opportunities WHERE id='op-ob'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("outbox persisted = %d err=%v", count, err)
	}
	var linked string
	if err := s.Pool.QueryRow(ctx, `SELECT opportunity_id FROM paper_cycles WHERE id='cyc-ob'`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != "op-ob" {
		t.Fatalf("cycle→opportunity linkage = %q", linked)
	}
	if ob.Dropped() != 0 {
		t.Fatalf("dropped = %d", ob.Dropped())
	}
}

// TestRiskEventPersistence covers BL-31: breaker transitions and risk
// rejections both persist through InsertRiskEvent and come back
// newest-first, windowed, from ListRiskEvents; the outbox routes
// "risk_event" records the same way it already routes opportunities and
// cycles.
func TestRiskEventPersistence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	if err := s.InsertRiskEvent(ctx, RiskEvent{
		ID: "re-1", TS: t0, Kind: "breaker_transition",
		Subject: "exchange:binance", LimitName: "consecutive_losses",
		Observed: "CLOSED", Threshold: "OPEN", Action: "5 consecutive losses",
		BreakerState: "OPEN",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertRiskEvent(ctx, RiskEvent{
		ID: "re-2", TS: t0.Add(time.Minute), Kind: "risk_reject",
		Subject: "triangle:tri-1", LimitName: "max_daily_loss",
		Observed: "-120", Threshold: "-100", Action: "risk rejected",
	}); err != nil {
		t.Fatal(err)
	}
	// Re-inserting the same id is idempotent (ON CONFLICT DO NOTHING),
	// matching InsertOpportunity/InsertCycle's contract.
	if err := s.InsertRiskEvent(ctx, RiskEvent{ID: "re-1", TS: t0, Kind: "breaker_transition"}); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ListRiskEvents(ctx, t0.Add(-time.Hour), t0.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	// Newest first.
	if rows[0].ID != "re-2" || rows[1].ID != "re-1" {
		t.Fatalf("order = %s, %s", rows[0].ID, rows[1].ID)
	}
	if rows[0].Kind != "risk_reject" || rows[0].LimitName == nil || *rows[0].LimitName != "max_daily_loss" {
		t.Fatalf("row 0 = %+v", rows[0])
	}
	if rows[1].BreakerState == nil || *rows[1].BreakerState != "OPEN" {
		t.Fatalf("row 1 breaker_state = %+v", rows[1])
	}

	// Window excludes events outside [from, to).
	narrow, err := s.ListRiskEvents(ctx, t0.Add(-time.Hour), t0.Add(30*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(narrow) != 1 || narrow[0].ID != "re-1" {
		t.Fatalf("narrow window = %+v", narrow)
	}

	// Outbox routing: enqueue a risk_event record and confirm it lands.
	ob := &Outbox{Store: s, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), FlushInterval: 10 * time.Millisecond}
	if !ob.Enqueue(Record{Kind: "risk_event", RiskEvent: &RiskEvent{
		ID: "re-3", TS: t0.Add(2 * time.Minute), Kind: "breaker_transition", Subject: "global",
	}}) {
		t.Fatal("enqueue refused")
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- ob.Run(runCtx) }()
	deadline := time.After(3 * time.Second)
	for ob.Written() < 1 {
		select {
		case <-deadline:
			t.Fatal("outbox never wrote the risk event")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM risk_events WHERE id='re-3'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("outbox persisted = %d err=%v", count, err)
	}
}

func clientIP() (a netip.Addr) { return netip.MustParseAddr("203.0.113.1") }

// Acceptance (audit CR-P1-4): hot-path producers may hit a zero-value
// outbox concurrently; lazy init must create exactly one queue so no
// record lands in an orphan channel (run with -race).
func TestOutboxConcurrentEnqueueRaceFree(t *testing.T) {
	ob := &Outbox{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var wg sync.WaitGroup
	const producers, each = 8, 50
	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				ob.Enqueue(Record{Kind: "cycle", Cycle: &execution.CycleResult{}})
			}
		}()
	}
	wg.Wait()
	if got := len(ob.ch); got != producers*each {
		t.Fatalf("queued = %d, want %d (records lost to a second channel?)", got, producers*each)
	}
}
