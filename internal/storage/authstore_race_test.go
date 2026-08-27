package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
)

// TestAuthStoreUpdateUserRoleLastAdminRaceIsAtomic is the P2-5 pgx
// regression test: two admins demoted CONCURRENTLY through the SAME
// AuthStore must not both succeed (that would leave zero enabled
// admins). Before the fix, the last-admin guard lived entirely in
// AdminService (list-then-check, a separate round trip from the write),
// so two concurrent requests against two different admins could each
// see "the other one is still an admin" and both write successfully.
// AuthStore.UpdateUserRole now enforces the guard with a conditional
// UPDATE inside one transaction, locking the target row and the
// currently-enabled-admin set in one deterministic (ORDER BY id)
// statement — run with -race.
func TestAuthStoreUpdateUserRoleLastAdminRaceIsAtomic(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	as := s.Auth()

	mk := func(id, email string, role auth.Role) auth.User {
		hash, err := auth.HashPassword("a-strong-password")
		if err != nil {
			t.Fatal(err)
		}
		u := auth.User{ID: id, Email: email, PasswordHash: hash, Role: role}
		if err := as.UpsertUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	a := mk("admin-a", "race-a@example.test", auth.RoleAdmin)
	b := mk("admin-b", "race-b@example.test", auth.RoleAdmin)

	start := make(chan struct{})
	results := make(chan error, 2)
	demote := func(id string) {
		<-start
		results <- as.UpdateUserRole(ctx, id, auth.RoleOperator)
	}
	go demote(a.ID)
	go demote(b.ID)
	close(start)

	r1, r2 := <-results, <-results
	successes, lastAdminRefusals := 0, 0
	for _, err := range []error{r1, r2} {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, auth.ErrLastAdmin):
			lastAdminRefusals++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || lastAdminRefusals != 1 {
		t.Fatalf("concurrent demotions: successes=%d refusals=%d (want exactly one of each)", successes, lastAdminRefusals)
	}

	var admins int
	if err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FROM users WHERE role = 'ADMIN' AND status <> 'disabled'`).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if admins != 1 {
		t.Fatalf("enabled admins after the race = %d, want 1", admins)
	}
}
