package tenancy

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A member of both the platform organisation and a tenant organisation
// acts in the tenant organisation by default, however much older the
// platform membership is; the platform is the default only when it is
// the sole membership.
func TestContextsForUserPrefersTenantOverPlatform(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := m.AddMember(ctx, Membership{OrgID: PlatformOrgID, UserID: "dual", Role: RoleAdmin, CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	org, err := m.CreateOrg(ctx, Org{Name: "Tenant", CreatedAt: t0.Add(time.Hour)}, "dual")
	if err != nil {
		t.Fatal(err)
	}
	cs, err := m.ContextsForUser(ctx, "dual")
	if err != nil || len(cs) != 2 || cs[0].Org.ID != org.ID || cs[1].Org.ID != PlatformOrgID {
		t.Fatalf("contexts = %+v err=%v", cs, err)
	}
	if c, err := m.ContextForUser(ctx, "dual"); err != nil || c.Org.ID != org.ID || c.Membership.Role != RoleOwner {
		t.Fatalf("default context = %+v err=%v", c, err)
	}
	// Two tenant memberships: the older one is the default.
	older, _ := m.CreateOrg(ctx, Org{Name: "Older", CreatedAt: t0.Add(-time.Hour)}, "")
	_ = m.AddMember(ctx, Membership{OrgID: older.ID, UserID: "dual", Role: RoleViewer, CreatedAt: t0.Add(-time.Hour)})
	if cs, _ := m.ContextsForUser(ctx, "dual"); len(cs) != 3 || cs[0].Org.ID != older.ID || cs[1].Org.ID != org.ID || cs[2].Org.ID != PlatformOrgID {
		t.Fatalf("three contexts = %+v", cs)
	}
	// Staff with only the platform membership stay on the platform.
	_ = m.AddMember(ctx, Membership{OrgID: PlatformOrgID, UserID: "staff", Role: RoleAdmin, CreatedAt: t0})
	if c, err := m.ContextForUser(ctx, "staff"); err != nil || c.Org.ID != PlatformOrgID {
		t.Fatalf("staff context = %+v err=%v", c, err)
	}
	if _, err := m.ContextForUser(ctx, "nobody"); !errors.Is(err, ErrNoMembership) {
		t.Fatalf("nobody = %v", err)
	}
	ids, err := m.ListOrgIDs(ctx)
	if err != nil || len(ids) != 3 || ids[0] != PlatformOrgID || ids[1] != org.ID || ids[2] != older.ID {
		t.Fatalf("org ids = %v err=%v", ids, err)
	}
}
