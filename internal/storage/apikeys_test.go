package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/apikey"
	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

func TestAPIKeysStoreCRUD(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedUser(t, s, "u-key-owner", "key-owner@example.test", auth.RoleAdmin)
	ks := s.APIKeys()

	rec, plaintext, err := apikey.Generate(1, "u-key-owner", "CI pipeline", []string{"read", "rules:write"})
	if err != nil {
		t.Fatal(err)
	}
	rec.ID = "key-1"
	created, err := ks.CreateKey(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if created.Prefix != rec.Prefix || created.Hash != rec.Hash || len(created.Scopes) != 2 {
		t.Fatalf("CreateKey = %+v", created)
	}
	if plaintext == "" {
		t.Fatal("Generate must return a plaintext token")
	}

	// Authenticate resolves the plaintext through the store.
	got, ok, err := apikey.Authenticate(ctx, ks, plaintext)
	if err != nil || !ok || got.ID != "key-1" {
		t.Fatalf("Authenticate = %+v %v %v", got, ok, err)
	}
	// A tampered token never authenticates.
	if _, ok, err := apikey.Authenticate(ctx, ks, plaintext[:len(plaintext)-1]+"x"); ok || err != nil {
		t.Fatalf("tampered token authenticated: ok=%v err=%v", ok, err)
	}

	list, err := ks.ListKeys(ctx, 1)
	if err != nil || len(list) != 1 || list[0].ID != "key-1" {
		t.Fatalf("ListKeys = %+v err=%v", list, err)
	}

	if err := ks.Touch(ctx, "key-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	list, _ = ks.ListKeys(ctx, 1)
	if list[0].LastUsedAt == nil {
		t.Fatal("Touch did not persist last_used_at")
	}

	if err := ks.RevokeKey(ctx, 1, "key-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := apikey.Authenticate(ctx, ks, plaintext); ok || err != nil {
		t.Fatalf("revoked key authenticated: ok=%v err=%v", ok, err)
	}
	// Revoking again (already revoked) reports not found.
	if err := ks.RevokeKey(ctx, 1, "key-1", time.Now().UTC()); err == nil {
		t.Fatal("revoking an already-revoked key must fail")
	}
	// Revoking under the wrong org is refused.
	rec2, _, err := apikey.Generate(1, "u-key-owner", "second key", []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	rec2.ID = "key-2"
	if _, err := ks.CreateKey(ctx, rec2); err != nil {
		t.Fatal(err)
	}
	if err := ks.RevokeKey(ctx, 999, "key-2", time.Now().UTC()); err == nil {
		t.Fatal("revoking under the wrong org must fail")
	}
}

func TestAPIKeysByPrefixNotFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.APIKeys().ByPrefix(ctx, "arbk_nosuch"); err != apikey.ErrNotFound {
		t.Fatalf("ByPrefix unknown = %v", err)
	}
}

// Acceptance (audit S3/P1-12): RevokeByOwner revokes every key the
// account holds, across every organisation; RevokeByMembership revokes
// only the keys scoped to one organisation, leaving the account's
// other keys (in a different organisation) live.
func TestAPIKeysCascadeRevoke(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedUser(t, s, "u-cascade", "cascade@example.test", auth.RoleAdmin)
	ks := s.APIKeys()

	org2, err := s.Tenancy().CreateOrg(ctx, tenancy.Org{Name: "Second Org", PackageCode: "signal", RiskAckVersion: "1"}, "u-cascade")
	if err != nil {
		t.Fatal(err)
	}

	mustKey := func(id string, orgID int64) {
		rec, _, err := apikey.Generate(orgID, "u-cascade", id, []string{"read"})
		if err != nil {
			t.Fatal(err)
		}
		rec.ID = id
		if _, err := ks.CreateKey(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	mustKey("key-org1-a", 1)
	mustKey("key-org1-b", 1)
	mustKey("key-org2-a", org2.ID)

	isRevoked := func(id string) bool {
		list, err := ks.ListKeys(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		list2, err := ks.ListKeys(ctx, org2.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range append(list, list2...) {
			if k.ID == id {
				return k.RevokedAt != nil
			}
		}
		t.Fatalf("key %s not found", id)
		return false
	}

	// RevokeByMembership(org2, ...) only touches the org-2 key.
	n, err := ks.RevokeByMembership(ctx, org2.ID, "u-cascade", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("RevokeByMembership revoked %d keys, want 1", n)
	}
	if !isRevoked("key-org2-a") {
		t.Fatal("key-org2-a was not revoked")
	}
	if isRevoked("key-org1-a") || isRevoked("key-org1-b") {
		t.Fatal("RevokeByMembership touched a key outside the target organisation")
	}
	// Calling it again finds nothing left to revoke.
	if n, err := ks.RevokeByMembership(ctx, org2.ID, "u-cascade", time.Now().UTC()); err != nil || n != 0 {
		t.Fatalf("second RevokeByMembership = %d, %v, want 0, nil", n, err)
	}

	// RevokeByOwner sweeps every remaining live key for the account,
	// regardless of organisation.
	n, err = ks.RevokeByOwner(ctx, "u-cascade", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("RevokeByOwner revoked %d keys, want 2 (the two org-1 keys)", n)
	}
	if !isRevoked("key-org1-a") || !isRevoked("key-org1-b") {
		t.Fatal("RevokeByOwner left a key live")
	}
	// An account with no keys at all revokes zero, not an error.
	if n, err := ks.RevokeByOwner(ctx, "no-such-user", time.Now().UTC()); err != nil || n != 0 {
		t.Fatalf("RevokeByOwner for an unknown user = %d, %v, want 0, nil", n, err)
	}
}
