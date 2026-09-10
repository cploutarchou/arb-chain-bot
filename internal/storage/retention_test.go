package storage

import (
	"context"
	"testing"
	"time"
)

// seedOpportunity inserts one exchanges/triangles/opportunities chain
// with the given id, status and age (detected_at = now - age).
func seedOpportunityForRetention(t *testing.T, s *Store, id, status string, age time.Duration) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO exchanges (id, name) VALUES ('binance','binance') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO triangles (id, exchange_id, starting_asset, legs, canonical_key)
		VALUES ('tri-ret','binance','USDT','[]'::jsonb,'tri-ret') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO opportunities (id, exchange_id, triangle_id, status, starting_asset, starting_amount, legs, detected_at)
		VALUES ($1,'binance','tri-ret',$2,'USDT',1000,'[]'::jsonb,$3)`,
		id, status, time.Now().Add(-age)); err != nil {
		t.Fatal(err)
	}
}

func opportunityExists(t *testing.T, s *Store, id string) bool {
	t.Helper()
	var exists bool
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM opportunities WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

// TestPruneOpportunitiesRespectsStatusWindowAndCycleLink covers the
// three things the "opportunities_qualified"/"opportunities_rejected"
// rules must get right: a row inside its window survives, a row outside
// it is removed, and a row outside its window that a (permanently kept)
// paper_cycles row still references is never removed regardless of age.
func TestPruneOpportunitiesRespectsStatusWindowAndCycleLink(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()

	seedOpportunityForRetention(t, s, "op-ret-old-q-unlinked", "QUALIFIED", 100*24*time.Hour)
	seedOpportunityForRetention(t, s, "op-ret-old-q-linked", "QUALIFIED", 100*24*time.Hour)
	seedOpportunityForRetention(t, s, "op-ret-new-q", "QUALIFIED", 24*time.Hour)
	seedOpportunityForRetention(t, s, "op-ret-old-rejected", "REJECTED", 20*24*time.Hour)
	seedOpportunityForRetention(t, s, "op-ret-new-rejected", "REJECTED", 24*time.Hour)

	// A cycle (financial evidence, kept forever) references the "linked"
	// opportunity: the FK from paper_cycles.opportunity_id has no cascade,
	// so pruning must skip it even though it is well outside the window.
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO paper_sessions (id, mode, starting_balances) VALUES ('sess-ret','PAPER','{}'::jsonb)
		ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO paper_cycles (id, session_id, opportunity_id, outcome, started_at)
		VALUES ('cyc-ret','sess-ret','op-ret-old-q-linked','ALL_FILLED', now())`); err != nil {
		t.Fatal(err)
	}

	qualifiedCutoff := now.Add(-90 * 24 * time.Hour)
	rejectedCutoff := now.Add(-14 * 24 * time.Hour)

	n, err := s.CountPrunable(ctx, "opportunities_qualified", qualifiedCutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("CountPrunable(qualified) = %d, want 1 (only the unlinked one)", n)
	}
	deleted, err := s.PruneBatch(ctx, "opportunities_qualified", qualifiedCutoff, 500)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("PruneBatch(qualified) = %d, want 1", deleted)
	}
	if opportunityExists(t, s, "op-ret-old-q-unlinked") {
		t.Fatal("old, unlinked, out-of-window qualified opportunity survived")
	}
	if !opportunityExists(t, s, "op-ret-old-q-linked") {
		t.Fatal("old qualified opportunity referenced by a paper_cycles row was deleted")
	}
	if !opportunityExists(t, s, "op-ret-new-q") {
		t.Fatal("in-window qualified opportunity was deleted")
	}

	n, err = s.CountPrunable(ctx, "opportunities_rejected", rejectedCutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("CountPrunable(rejected) = %d, want 1", n)
	}
	deleted, err = s.PruneBatch(ctx, "opportunities_rejected", rejectedCutoff, 500)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("PruneBatch(rejected) = %d, want 1", deleted)
	}
	if opportunityExists(t, s, "op-ret-old-rejected") {
		t.Fatal("out-of-window rejected opportunity survived")
	}
	if !opportunityExists(t, s, "op-ret-new-rejected") {
		t.Fatal("in-window rejected opportunity was deleted")
	}
}

// TestPruneExchangeHealthAndSystemEvents covers the two plain
// timestamp-cutoff rules with no secondary guard.
func TestPruneExchangeHealthAndSystemEvents(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := s.Pool.Exec(ctx, `INSERT INTO exchanges (id, name) VALUES ('binance','binance') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO exchange_health (exchange_id, ts, feed_state) VALUES
		('binance', $1, 'CONNECTED'),
		('binance', $2, 'CONNECTED')`, now.Add(-40*24*time.Hour), now.Add(-1*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO system_events (id, ts, component, kind) VALUES
		('se-ret-old', $1, 'engine', 'restart'),
		('se-ret-new', $2, 'engine', 'restart')`, now.Add(-40*24*time.Hour), now.Add(-1*time.Hour)); err != nil {
		t.Fatal(err)
	}

	cutoff := now.Add(-30 * 24 * time.Hour)

	if n, err := s.CountPrunable(ctx, "exchange_health", cutoff); err != nil || n != 1 {
		t.Fatalf("CountPrunable(exchange_health) = %d err=%v, want 1", n, err)
	}
	if deleted, err := s.PruneBatch(ctx, "exchange_health", cutoff, 500); err != nil || deleted != 1 {
		t.Fatalf("PruneBatch(exchange_health) = %d err=%v, want 1", deleted, err)
	}
	var remaining int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM exchange_health`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("exchange_health rows left = %d, want 1", remaining)
	}

	if n, err := s.CountPrunable(ctx, "system_events", cutoff); err != nil || n != 1 {
		t.Fatalf("CountPrunable(system_events) = %d err=%v, want 1", n, err)
	}
	if deleted, err := s.PruneBatch(ctx, "system_events", cutoff, 500); err != nil || deleted != 1 {
		t.Fatalf("PruneBatch(system_events) = %d err=%v, want 1", deleted, err)
	}
	var seID string
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM system_events`).Scan(&seID); err != nil {
		t.Fatal(err)
	}
	if seID != "se-ret-new" {
		t.Fatalf("surviving system_events row = %s, want se-ret-new", seID)
	}
}

// TestPruneFundingHistory exercises the composite-primary-key rule
// (venue, base, at) — the one rule with no surrogate id column.
func TestPruneFundingHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO funding_history (venue, base, at, rate) VALUES
		('binance','BTC',$1,'0.0001'),
		('binance','BTC',$2,'0.0002')`, now.Add(-200*24*time.Hour), now.Add(-1*time.Hour)); err != nil {
		t.Fatal(err)
	}

	cutoff := now.Add(-180 * 24 * time.Hour)
	if n, err := s.CountPrunable(ctx, "funding_history", cutoff); err != nil || n != 1 {
		t.Fatalf("CountPrunable(funding_history) = %d err=%v, want 1", n, err)
	}
	if deleted, err := s.PruneBatch(ctx, "funding_history", cutoff, 500); err != nil || deleted != 1 {
		t.Fatalf("PruneBatch(funding_history) = %d err=%v, want 1", deleted, err)
	}
	var remainingAt time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT at FROM funding_history`).Scan(&remainingAt); err != nil {
		t.Fatal(err)
	}
	if !remainingAt.After(cutoff) {
		t.Fatalf("surviving row at %s is not after cutoff %s", remainingAt, cutoff)
	}
}

// TestPruneSessions covers the expression-based cutoff
// (COALESCE(revoked_at, expires_at)): a long-revoked session and a
// long-expired-but-never-revoked session both age out; a live session
// (future expiry, never revoked) survives no matter how old the window.
func TestPruneSessions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role)
		VALUES ('u-ret','ret@example.test','Ret','x','VIEWER')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, created_at, expires_at, revoked_at) VALUES
		('sess-ret-revoked','u-ret',$1,$2,$3),
		('sess-ret-expired','u-ret',$1,$4,NULL),
		('sess-ret-live','u-ret',$1,$5,NULL)`,
		now.Add(-60*24*time.Hour),           // created_at (irrelevant to the rule)
		now.Add(-45*24*time.Hour),           // sess-ret-revoked: expires_at (superseded by revoked_at below)
		now.Add(-40*24*time.Hour),           // sess-ret-revoked: revoked_at — old
		now.Add(-40*24*time.Hour),           // sess-ret-expired: expires_at — old, never revoked
		now.Add(24*time.Hour)); err != nil { // sess-ret-live: expires_at — in the future
		t.Fatal(err)
	}

	cutoff := now.Add(-30 * 24 * time.Hour)
	if n, err := s.CountPrunable(ctx, "sessions", cutoff); err != nil || n != 2 {
		t.Fatalf("CountPrunable(sessions) = %d err=%v, want 2", n, err)
	}
	if deleted, err := s.PruneBatch(ctx, "sessions", cutoff, 500); err != nil || deleted != 2 {
		t.Fatalf("PruneBatch(sessions) = %d err=%v, want 2", deleted, err)
	}
	var liveID string
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM sessions`).Scan(&liveID); err != nil {
		t.Fatal(err)
	}
	if liveID != "sess-ret-live" {
		t.Fatalf("surviving session = %s, want sess-ret-live", liveID)
	}
}

// TestPruneRecordingMetadataSkipsOpenSessions confirms a recording
// session with no ended_at (still running, or crashed without a clean
// close) is never eligible no matter how old started_at is.
func TestPruneRecordingMetadataSkipsOpenSessions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := s.Pool.Exec(ctx, `INSERT INTO exchanges (id, name) VALUES ('binance','binance') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO market_recording_metadata (id, exchange_id, started_at, ended_at, streams) VALUES
		('rec-ret-closed-old','binance',$1,$2,'{}'::jsonb),
		('rec-ret-open','binance',$1,NULL,'{}'::jsonb)`,
		now.Add(-400*24*time.Hour), now.Add(-370*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	cutoff := now.Add(-180 * 24 * time.Hour)
	if n, err := s.CountPrunable(ctx, "recording_metadata", cutoff); err != nil || n != 1 {
		t.Fatalf("CountPrunable(recording_metadata) = %d err=%v, want 1", n, err)
	}
	if deleted, err := s.PruneBatch(ctx, "recording_metadata", cutoff, 500); err != nil || deleted != 1 {
		t.Fatalf("PruneBatch(recording_metadata) = %d err=%v, want 1", deleted, err)
	}
	var remainingID string
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM market_recording_metadata`).Scan(&remainingID); err != nil {
		t.Fatal(err)
	}
	if remainingID != "rec-ret-open" {
		t.Fatalf("surviving row = %s, want the still-open session", remainingID)
	}
}

// TestPruneBatchIsBounded seeds more eligible rows than one batch and
// confirms PruneBatch never removes more than batchSize at a time, and
// reports a count below batchSize once the rule is exhausted.
func TestPruneBatchIsBounded(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()

	old := now.Add(-40 * 24 * time.Hour)
	for i := 0; i < 5; i++ {
		id := "se-ret-batch-" + string(rune('a'+i))
		if _, err := s.Pool.Exec(ctx, `
			INSERT INTO system_events (id, ts, component, kind) VALUES ($1,$2,'engine','restart')`,
			id, old); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := now.Add(-30 * 24 * time.Hour)
	var total int64
	for i, want := range []int64{2, 2, 1} {
		deleted, err := s.PruneBatch(ctx, "system_events", cutoff, 2)
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		if deleted != want {
			t.Fatalf("batch %d: deleted = %d, want %d", i, deleted, want)
		}
		if deleted > 2 {
			t.Fatalf("batch %d: deleted %d rows, batch size was 2", i, deleted)
		}
		total += deleted
	}
	if total != 5 {
		t.Fatalf("total deleted = %d, want 5", total)
	}
	var remaining int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM system_events WHERE ts < $1`, cutoff).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("remaining eligible rows = %d, want 0", remaining)
	}
}
