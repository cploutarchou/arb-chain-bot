package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/apikey"
	"github.com/cploutarchou/arb-chain-bot/internal/auth"
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
