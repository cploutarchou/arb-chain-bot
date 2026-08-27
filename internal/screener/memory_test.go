package screener

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryRuleStoreCRUD(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryRuleStore()
	bps := d("50")
	r := Rule{ID: "r1", Name: "test", Kind: RuleKindSpread, MinSpreadBps: &bps}
	if _, err := store.InsertRule(ctx, r, "u1"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRule(ctx, "r1")
	if err != nil || got.Name != "test" {
		t.Fatalf("GetRule = %+v, %v", got, err)
	}
	got.Name = "updated"
	if _, err := store.UpdateRule(ctx, got, "u2"); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListRules(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "updated" {
		t.Fatalf("ListRules = %+v, %v", list, err)
	}
	if err := store.DeleteRule(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRule(ctx, "r1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMemoryEventStoreIdempotentInsertAndFilter(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryEventStore()
	e1 := Event{ID: "e1", RuleID: "r1", OpenedAt: time.Unix(1, 0)}
	e2 := Event{ID: "e2", RuleID: "r2", OpenedAt: time.Unix(2, 0)}
	if err := store.InsertEvent(ctx, e1); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertEvent(ctx, e1); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := store.InsertEvent(ctx, e2); err != nil {
		t.Fatal(err)
	}
	all, err := store.ListEvents(ctx, "", 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListEvents(all) = %+v, %v", all, err)
	}
	if all[0].ID != "e2" { // newest first
		t.Fatalf("order = %+v, want e2 first", all)
	}
	filtered, err := store.ListEvents(ctx, "r1", 10)
	if err != nil || len(filtered) != 1 || filtered[0].ID != "e1" {
		t.Fatalf("ListEvents(r1) = %+v, %v", filtered, err)
	}
}

func TestMemoryTemplateStorePerUser(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryTemplateStore()
	if _, err := store.InsertTemplate(ctx, Template{ID: "t1", UserID: "u1", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListTemplates(ctx, "u1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListTemplates(u1) = %+v, %v", list, err)
	}
	other, err := store.ListTemplates(ctx, "u2")
	if err != nil || len(other) != 0 {
		t.Fatalf("ListTemplates(u2) = %+v, %v", other, err)
	}
	if err := store.DeleteTemplate(ctx, "u2", "t1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete by wrong user = %v, want ErrNotFound", err)
	}
	if err := store.DeleteTemplate(ctx, "u1", "t1"); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryFundingStoreUpsertAndList(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryFundingStore()
	t0 := time.Unix(1_800_000_000, 0).UTC()
	if err := store.UpsertFunding(ctx, VenueBinance, "BTC", t0, "0.0001"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFunding(ctx, VenueBinance, "BTC", t0, "0.0002"); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := store.UpsertFunding(ctx, VenueBinance, "BTC", t0.Add(8*time.Hour), "0.0003"); err != nil {
		t.Fatal(err)
	}
	series, err := store.ListFunding(ctx, "BTC", nil, t0.Add(-time.Hour))
	if err != nil || len(series) != 1 || len(series[0].Points) != 2 {
		t.Fatalf("ListFunding = %+v, %v", series, err)
	}
	if series[0].Points[0].Rate != "0.0001" {
		t.Fatalf("first point rate = %s, want 0.0001 (ON CONFLICT DO NOTHING semantics)", series[0].Points[0].Rate)
	}
}
