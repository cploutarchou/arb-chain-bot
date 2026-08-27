package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
)

// testStoreForAPI opens a real Store against a disposable test Postgres
// (never the dev-compose DB on :5432); skips when ARB_TEST_DATABASE_URL
// is unset, mirroring internal/storage's own integration-test gate.
func testStoreForAPI(t *testing.T) *storage.Store {
	t.Helper()
	dsn := os.Getenv("ARB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ARB_TEST_DATABASE_URL not set; skipping DB-backed API test")
	}
	s, err := storage.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	for _, table := range []string{"fills", "orders", "paper_cycles", "paper_sessions",
		"opportunities", "triangles", "markets", "exchanges"} {
		if _, err := s.Pool.Exec(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	return s
}

// TestOpportunityDetailRouteAndPrecedence covers BL-27's route end to
// end against a real store, and the /history vs /{id} pattern
// precedence Go's ServeMux resolves by specificity (a literal segment
// beats a wildcard in the same position) — asserted here because a
// regression would silently route "history" through the {id} handler
// and 404 with "opportunity not found" instead of listing history.
func TestOpportunityDetailRouteAndPrecedence(t *testing.T) {
	store := testStoreForAPI(t)
	s, mux := newTestServer(t)
	s.Store = store
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	q := pricing.CycleQuote{
		Triangle: "binance|USDT|A>B>C", Start: "USDT",
		InputConsumed: decimal.RequireFromString("1000"), FinalAmount: decimal.RequireFromString("1010"),
	}
	op := opportunity.Build("op-route", "binance", q, opportunity.Buffers{}, time.Hour, time.Now(), 1)
	dec := risk.Decision{Allowed: true}
	if err := store.InsertOpportunity(context.Background(), &op, &dec); err != nil {
		t.Fatal(err)
	}

	// /history must still list, not 404 through the {id} handler.
	rec := getWith(t, mux, cookie, "/api/v1/opportunities/history")
	if rec.Code != http.StatusOK {
		t.Fatalf("history = %d: %s", rec.Code, rec.Body.String())
	}

	// /{id} for the real row.
	rec = getWith(t, mux, cookie, "/api/v1/opportunities/op-route")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data storage.OpportunityDetail `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.ID != "op-route" || env.Data.TriangleID != "binance|USDT|A>B>C" {
		t.Fatalf("detail = %+v", env.Data)
	}

	// Unknown id is a real 404, not the history list.
	rec = getWith(t, mux, cookie, "/api/v1/opportunities/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d: %s", rec.Code, rec.Body.String())
	}
}
