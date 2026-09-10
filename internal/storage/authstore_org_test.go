package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// TestAuthStoreCreateUserOrgPlacement covers the S4 follow-up: a
// console-created account joins the organisation the operator named,
// not organisation 1 — and the historical default (no org named) still
// lands in the platform organisation with the console-role mapping.
func TestAuthStoreCreateUserOrgPlacement(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	as := s.Auth()
	tn := s.Tenancy()

	owner, err := auth.HashPassword("owner-password-long")
	if err != nil {
		t.Fatal(err)
	}
	if err := as.UpsertUser(ctx, auth.User{
		ID: "u-owner", Email: "owner@x.test", PasswordHash: owner,
		Role: auth.RoleAdmin, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	org, err := tn.CreateOrg(ctx, tenancy.Org{Name: "acme", PackageCode: "watch", CustomerType: tenancy.CustomerBusiness}, "u-owner")
	if err != nil {
		t.Fatal(err)
	}

	hash, err := auth.HashPassword("tenant-password-long")
	if err != nil {
		t.Fatal(err)
	}
	u := auth.User{
		ID: "u-tenant", Email: "tenant-op@x.test", PasswordHash: hash,
		Role: auth.RoleOperator, CreatedAt: time.Now().UTC(),
		JoinOrgID: org.ID, JoinOrgRole: "ADMIN",
	}
	if err := as.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	members, err := tn.ListMembers(ctx, org.ID)
	if err != nil {
		t.Fatalf("tenant org members: %v", err)
	}
	var tenantMem *tenancy.Membership
	for i, m := range members {
		if m.UserID == "u-tenant" {
			tenantMem = &members[i]
		}
	}
	if tenantMem == nil {
		t.Fatalf("tenant account missing from its organisation: %+v", members)
	}
	if tenantMem.Role != tenancy.RoleAdmin {
		t.Fatalf("membership = %+v", tenantMem)
	}
	platform, err := tn.ListMembers(ctx, tenancy.PlatformOrgID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range platform {
		if m.UserID == "u-tenant" {
			t.Fatalf("tenant account also joined the platform organisation: %+v", m)
		}
	}

	// Default placement (no org named): platform organisation, console
	// role mapped (OPERATOR → ADMIN membership).
	plain := auth.User{
		ID: "u-staff", Email: "staff@x.test", PasswordHash: hash,
		Role: auth.RoleOperator, CreatedAt: time.Now().UTC(),
	}
	if err := as.CreateUser(ctx, plain); err != nil {
		t.Fatal(err)
	}
	platform, err = tn.ListMembers(ctx, tenancy.PlatformOrgID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range platform {
		if m.UserID == "u-staff" {
			found = true
			if m.Role != tenancy.RoleAdmin {
				t.Fatalf("staff membership role = %s", m.Role)
			}
		}
	}
	if !found {
		t.Fatal("default placement missing from the platform organisation")
	}

	// Defensive guards: OWNER and unknown orgs never reach the insert.
	bad := u
	bad.ID, bad.Email, bad.JoinOrgRole = "u-bad", "bad@x.test", "OWNER"
	if err := as.CreateUser(ctx, bad); err == nil {
		t.Fatal("OWNER membership accepted by the store")
	}
	unknown := u
	unknown.ID, unknown.Email, unknown.JoinOrgID = "u-ghost", "ghost@x.test", 99999
	if err := as.CreateUser(ctx, unknown); err == nil {
		t.Fatal("unknown organisation accepted by the store")
	}
}
