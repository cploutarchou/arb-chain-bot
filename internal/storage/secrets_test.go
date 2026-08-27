package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/secrets"
)

func TestSecretsStoreRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	st := s.Secrets()

	// updated_by references users(id): a real user row is attributed,
	// an unknown actor becomes NULL rather than failing the write.
	hash, _ := auth.HashPassword("pw")
	if err := s.Auth().UpsertUser(ctx, auth.User{ID: "u_1", Email: "a@example.test", PasswordHash: hash, Role: auth.RoleAdmin}); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := st.Get(ctx, "anthropic_api_key"); err != nil || ok {
		t.Fatalf("empty get = ok=%v err=%v", ok, err)
	}
	row := secrets.Row{
		Name: "anthropic_api_key", Ciphertext: []byte{1, 2, 3}, Nonce: []byte("twelve-bytes"),
		KeyID: "ab12cd34", UpdatedAt: t0, UpdatedBy: "u_1",
	}
	if err := st.Put(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.Get(ctx, "anthropic_api_key")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if string(got.Ciphertext) != "\x01\x02\x03" || string(got.Nonce) != "twelve-bytes" || got.KeyID != "ab12cd34" || got.UpdatedBy != "u_1" || !got.UpdatedAt.Equal(t0) {
		t.Fatalf("row = %+v", got)
	}

	// Overwrite (rotation) keeps one row; unknown actor → NULL.
	row.Ciphertext, row.UpdatedBy, row.UpdatedAt = []byte{9}, "system", t0.Add(time.Hour)
	if err := st.Put(ctx, row); err != nil {
		t.Fatal(err)
	}
	list, err := st.List(ctx)
	if err != nil || len(list) != 1 || string(list[0].Ciphertext) != "\x09" || list[0].UpdatedBy != "" {
		t.Fatalf("list = %+v err=%v", list, err)
	}

	// Round trip through the real vault against this store.
	key := make([]byte, 32)
	v, err := secrets.NewVault(st, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Put(ctx, "telegram_bot_token", []byte("telegram-token-value-12345678"), "u_1", t0); err != nil {
		t.Fatal(err)
	}
	val, src, ok := v.Get(ctx, "telegram_bot_token")
	if !ok || src != "vault" || val != "telegram-token-value-12345678" {
		t.Fatalf("vault get = %q %q %v", val, src, ok)
	}

	deleted, err := st.Delete(ctx, "anthropic_api_key")
	if err != nil || !deleted {
		t.Fatalf("delete = %v %v", deleted, err)
	}
	if deleted, _ := st.Delete(ctx, "anthropic_api_key"); deleted {
		t.Fatal("second delete must report no row")
	}
}

func TestSecretsStoreList(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	st := s.Secrets()
	if rows, err := st.List(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("empty list = %v %v", rows, err)
	}
	for _, name := range []string{"telegram_bot_token", "anthropic_api_key"} {
		if err := st.Put(ctx, secrets.Row{Name: name, Ciphertext: []byte{9}, Nonce: []byte("twelve-bytes"), KeyID: "k", UpdatedAt: t0, UpdatedBy: "nobody"}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.List(ctx)
	if err != nil || len(rows) != 2 || rows[0].Name != "anthropic_api_key" || rows[1].Name != "telegram_bot_token" {
		t.Fatalf("list = %+v %v", rows, err)
	}
	if rows[0].UpdatedBy != "" || !rows[0].UpdatedAt.Equal(t0) {
		t.Fatalf("row = %+v", rows[0])
	}
	// The manager's status view runs over this one call.
	v, _ := secrets.NewVault(st, make([]byte, 32))
	all, err := secrets.NewManager(v, "", nil).List(ctx)
	var list []secrets.Info
	for _, in := range all {
		if in.Group == secrets.GroupProvider {
			list = append(list, in)
		}
	}
	// Four provider entries since T-083 (the two Paddle secrets are
	// absent here); the stored anthropic row is first by name.
	if err != nil || len(list) != 4 || !list[0].Present || list[0].Source != "vault" || list[0].Readable {
		t.Fatalf("manager list = %+v %v", list, err)
	}
}
