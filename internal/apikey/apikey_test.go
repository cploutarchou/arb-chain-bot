package apikey

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The package had no tests before audit S3/P1-12; these cover the
// existing Generate/Authenticate contract plus the cascade-revoke
// methods the fix adds.

func TestGenerateVerifyAuthenticateRoundTrip(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	k, plaintext, err := Generate(1, "u1", "CI key", []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	k.ID = "k1"
	if _, err := store.CreateKey(ctx, k); err != nil {
		t.Fatal(err)
	}

	got, ok, err := Authenticate(ctx, store, plaintext)
	if err != nil || !ok || got.ID != "k1" {
		t.Fatalf("Authenticate = %+v ok=%v err=%v", got, ok, err)
	}
	// A key with the wrong prefix entirely, an unknown-but-well-formed
	// prefix, and a tampered secret all fail the same way: ok=false,
	// err=nil (no oracle between "not our token" and "wrong secret").
	for _, bad := range []string{
		"not-a-key-at-all",
		TokenPrefix + "0000000unknown0000000000000000000000000",
		plaintext[:len(plaintext)-1] + "x",
	} {
		if _, ok, err := Authenticate(ctx, store, bad); ok || err != nil {
			t.Fatalf("Authenticate(%q) = ok=%v err=%v, want ok=false err=nil", bad, ok, err)
		}
	}
}

func TestValidateScopes(t *testing.T) {
	if err := ValidateScopes(nil); !errors.Is(err, ErrNoScopes) {
		t.Fatalf("empty scopes = %v", err)
	}
	if err := ValidateScopes([]string{"read", "bogus"}); !errors.Is(err, ErrUnknownScope) {
		t.Fatalf("unknown scope = %v", err)
	}
	if err := ValidateScopes([]string{"read", "read"}); !errors.Is(err, ErrDuplicateScope) {
		t.Fatalf("duplicate scope = %v", err)
	}
	if err := ValidateScopes([]string{"read", "rules:write"}); err != nil {
		t.Fatalf("valid scopes rejected: %v", err)
	}
}

func TestRevokedKeyNeverAuthenticates(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	k, plaintext, err := Generate(1, "u1", "key", []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	k.ID = "k1"
	if _, err := store.CreateKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeKey(ctx, 1, "k1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Authenticate(ctx, store, plaintext); ok || err != nil {
		t.Fatalf("revoked key authenticated: ok=%v err=%v", ok, err)
	}
}

// Acceptance (audit S3/P1-12): RevokeByOwner revokes every live key a
// user holds across every organisation and leaves other users' keys
// alone.
func TestMemoryStoreRevokeByOwner(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	mustKey := func(id string, orgID int64, userID string) {
		k, _, err := Generate(orgID, userID, id, []string{"read"})
		if err != nil {
			t.Fatal(err)
		}
		k.ID = id
		if _, err := store.CreateKey(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	mustKey("a1", 1, "user-a")
	mustKey("a2", 2, "user-a")
	mustKey("b1", 1, "user-b")

	n, err := store.RevokeByOwner(ctx, "user-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("RevokeByOwner revoked %d, want 2", n)
	}
	if !keyRevoked(t, store, 1, "a1") || !keyRevoked(t, store, 2, "a2") {
		t.Fatal("user-a's keys were not revoked")
	}
	if keyRevoked(t, store, 1, "b1") {
		t.Fatal("user-b's key was touched by user-a's cascade")
	}
	// Nothing left for user-a: the second call revokes zero, not an error.
	if n, err := store.RevokeByOwner(ctx, "user-a", time.Now()); err != nil || n != 0 {
		t.Fatalf("second RevokeByOwner = %d, %v, want 0, nil", n, err)
	}
}

// Acceptance (audit S3/P1-12): RevokeByMembership only revokes the
// caller's keys scoped to the named organisation, leaving their keys
// in a different organisation live.
func TestMemoryStoreRevokeByMembership(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	mustKey := func(id string, orgID int64) {
		k, _, err := Generate(orgID, "user-a", id, []string{"read"})
		if err != nil {
			t.Fatal(err)
		}
		k.ID = id
		if _, err := store.CreateKey(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	mustKey("org1-a", 1)
	mustKey("org2-a", 2)

	n, err := store.RevokeByMembership(ctx, 1, "user-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("RevokeByMembership revoked %d, want 1", n)
	}
	if !keyRevoked(t, store, 1, "org1-a") {
		t.Fatal("org1-a not revoked")
	}
	if keyRevoked(t, store, 2, "org2-a") {
		t.Fatal("org2-a was touched by an org-1 membership removal")
	}
}

// keyRevoked reports whether the key with the given id, listed under
// orgID, currently carries a RevokedAt.
func keyRevoked(t *testing.T, store *MemoryStore, orgID int64, id string) bool {
	t.Helper()
	rows, err := store.ListKeys(context.Background(), orgID)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range rows {
		if k.ID == id {
			return k.RevokedAt != nil
		}
	}
	t.Fatalf("key %s not found under org %d", id, orgID)
	return false
}
