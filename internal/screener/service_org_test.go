package screener

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// A tenant organisation's document is seeded on first read, versioned
// and applied on its own, and never replaces the process-wide
// (platform) snapshot the engine loops run on nor triggers the
// executor's wallet reload; the platform path is unchanged.
func TestServiceTenantDocumentsAreSeparate(t *testing.T) {
	svc := NewService(NewBook(), NewMemoryStore(), discardLogger(), nil)
	ctx := context.Background()
	platform, err := svc.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	applied := 0
	svc.OnSettingsApplied = func(Snapshot) { applied++ }
	ctxA := tenancy.WithOrg(ctx, 2)
	ctxB := tenancy.WithOrg(ctx, 3)

	a, err := svc.SnapshotFor(ctxA)
	if err != nil || a.Version == 0 || a.Version == platform.Version || a.CreatedBy != "system" {
		t.Fatalf("seeded tenant snapshot = %+v err=%v", a, err)
	}
	if again, _ := svc.SnapshotFor(ctxA); again.Version != a.Version {
		t.Fatalf("second read re-seeded: %d then %d", a.Version, again.Version)
	}
	doc := a.Settings.Clone()
	doc.MinLiquidityQuote = decimal.NewFromInt(4242)
	// Building on the platform's version number is stale for the tenant.
	var stale *StaleVersionError
	if _, err := svc.ApplyExpect(ctxA, "ua", "web", doc, platform.Version); !errors.As(err, &stale) || stale.Current != a.Version {
		t.Fatalf("apply on platform version = %v", err)
	}
	next, err := svc.ApplyExpect(ctxA, "ua", "web", doc, a.Version)
	if err != nil || next.Version <= a.Version || next.ParentVer != a.Version {
		t.Fatalf("tenant apply = %+v err=%v", next, err)
	}
	if got, _ := svc.SnapshotFor(ctxA); got.Version != next.Version || !got.Settings.MinLiquidityQuote.Equal(decimal.NewFromInt(4242)) {
		t.Fatalf("tenant read after apply = %+v", got)
	}
	if cur := svc.Current(); cur.Version != platform.Version || cur.Settings.MinLiquidityQuote.Equal(decimal.NewFromInt(4242)) {
		t.Fatalf("tenant apply reached the platform snapshot: %+v", cur)
	}
	if unscoped, _ := svc.SnapshotFor(ctx); unscoped.Version != platform.Version {
		t.Fatalf("unscoped snapshot = %+v", unscoped)
	}
	if b, _ := svc.SnapshotFor(ctxB); b.Version == next.Version || b.Settings.MinLiquidityQuote.Equal(decimal.NewFromInt(4242)) {
		t.Fatalf("B sees A's document: %+v", b)
	}
	if applied != 0 {
		t.Fatalf("OnSettingsApplied fired %d times for tenant writes", applied)
	}
	// Version history is per organisation too.
	if _, err := svc.Get(ctxB, next.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B reads A's version: %v", err)
	}
	if list, _ := svc.List(ctxA, 10); len(list) != 2 || !list[0].Active || list[0].Version != next.Version {
		t.Fatalf("A history = %+v", list)
	}
	// The platform path still swaps the process-wide snapshot and
	// notifies the executor.
	pdoc := platform.Settings.Clone()
	pdoc.MinLiquidityQuote = decimal.NewFromInt(7)
	if _, err := svc.ApplyExpect(ctx, "op", "web", pdoc, platform.Version); err != nil {
		t.Fatal(err)
	}
	if applied != 1 || !svc.Current().Settings.MinLiquidityQuote.Equal(decimal.NewFromInt(7)) {
		t.Fatalf("platform apply: notified=%d current=%+v", applied, svc.Current().Settings.MinLiquidityQuote)
	}
}
