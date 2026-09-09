package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestAuditAndRiskEventsAreImmutable is the DB-backed proof for migration
// 000018 (docs/audit/database-audit.md D6, roadmap P1-19): INSERT must
// keep working exactly as before, and both UPDATE and DELETE must be
// rejected by the enforce_append_only() trigger with SQLSTATE P0001
// ("raise_exception"), on both audit_events and risk_events, leaving the
// original row untouched.
func TestAuditAndRiskEventsAreImmutable(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	if err := s.InsertAuditEvent(ctx, AuditRow{
		ID: "aud-immutable-1", Actor: "telegram:1", Source: "telegram",
		Action: "paper_pause", Entity: "paper_engine",
	}); err != nil {
		t.Fatalf("INSERT audit_events: %v", err)
	}
	if err := s.InsertRiskEvent(ctx, RiskEvent{
		ID: "re-immutable-1", TS: t0, Kind: "breaker_transition",
		Subject: "exchange:binance", BreakerState: "OPEN",
	}); err != nil {
		t.Fatalf("INSERT risk_events: %v", err)
	}

	assertBlocked := func(t *testing.T, label, sql string, args ...any) {
		t.Helper()
		_, err := s.Pool.Exec(ctx, sql, args...)
		if err == nil {
			t.Fatalf("%s: succeeded, want SQLSTATE P0001", label)
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Fatalf("%s: err = %v, want a *pgconn.PgError", label, err)
		}
		if pgErr.Code != "P0001" {
			t.Fatalf("%s: SQLSTATE = %s, want P0001 (error: %v)", label, pgErr.Code, err)
		}
	}

	assertBlocked(t, "UPDATE audit_events", `UPDATE audit_events SET action = 'tampered' WHERE id = $1`, "aud-immutable-1")
	assertBlocked(t, "DELETE audit_events", `DELETE FROM audit_events WHERE id = $1`, "aud-immutable-1")
	assertBlocked(t, "UPDATE risk_events", `UPDATE risk_events SET kind = 'tampered' WHERE id = $1`, "re-immutable-1")
	assertBlocked(t, "DELETE risk_events", `DELETE FROM risk_events WHERE id = $1`, "re-immutable-1")

	// Every rejected statement must have left the row exactly as inserted.
	var action string
	if err := s.Pool.QueryRow(ctx, `SELECT action FROM audit_events WHERE id = $1`, "aud-immutable-1").Scan(&action); err != nil {
		t.Fatalf("audit row missing after rejected mutations: %v", err)
	}
	if action != "paper_pause" {
		t.Fatalf("audit_events.action = %q, want unchanged %q", action, "paper_pause")
	}
	var kind string
	if err := s.Pool.QueryRow(ctx, `SELECT kind FROM risk_events WHERE id = $1`, "re-immutable-1").Scan(&kind); err != nil {
		t.Fatalf("risk_events row missing after rejected mutations: %v", err)
	}
	if kind != "breaker_transition" {
		t.Fatalf("risk_events.kind = %q, want unchanged %q", kind, "breaker_transition")
	}

	// A second, distinct id still inserts cleanly — the trigger only
	// fires on UPDATE/DELETE, never on INSERT.
	if err := s.InsertAuditEvent(ctx, AuditRow{
		ID: "aud-immutable-2", Actor: "system", Source: "system",
		Action: "paper_resume", Entity: "paper_engine",
	}); err != nil {
		t.Fatalf("second INSERT audit_events: %v", err)
	}
	if err := s.InsertRiskEvent(ctx, RiskEvent{
		ID: "re-immutable-2", TS: t0.Add(time.Minute), Kind: "risk_reject",
	}); err != nil {
		t.Fatalf("second INSERT risk_events: %v", err)
	}
}
