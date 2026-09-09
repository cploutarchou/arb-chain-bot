package tenancy

import (
	"context"
	"errors"
	"testing"
)

// Acceptance (audit S3/P1-12): MembershipFor is the exact-pair lookup
// the API-key authentication path uses to verify a key's owner still
// belongs to the key's organisation, independent of ContextForUser's
// "pick one" resolution.
func TestMemoryStoreMembershipFor(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()

	org, err := m.CreateOrg(ctx, Org{Name: "Org"}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := m.MembershipFor(ctx, org.ID, "owner")
	if err != nil || mem.Role != RoleOwner {
		t.Fatalf("MembershipFor(owner) = %+v err=%v", mem, err)
	}
	// A user who belongs to some OTHER organisation, but not this one.
	if err := m.AddMember(ctx, Membership{OrgID: PlatformOrgID, UserID: "staff", Role: RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MembershipFor(ctx, org.ID, "staff"); !errors.Is(err, ErrNoMembership) {
		t.Fatalf("MembershipFor(staff, wrong org) = %v, want ErrNoMembership", err)
	}
	// An organisation that does not exist at all.
	if _, err := m.MembershipFor(ctx, org.ID+1000, "owner"); !errors.Is(err, ErrUnknownOrg) {
		t.Fatalf("MembershipFor(unknown org) = %v, want ErrUnknownOrg", err)
	}

	// Removing the membership flips a live result to ErrNoMembership —
	// the exact transition the API-key authentication path depends on.
	if err := m.AddMember(ctx, Membership{OrgID: org.ID, UserID: "second-owner", Role: RoleOwner}); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveMember(ctx, org.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MembershipFor(ctx, org.ID, "owner"); !errors.Is(err, ErrNoMembership) {
		t.Fatalf("MembershipFor after removal = %v, want ErrNoMembership", err)
	}
}
