package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func adminService(t *testing.T) (*AdminService, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	var seq int
	nowFn := func() time.Time { return t0 }
	idFn := func() string { seq++; return "generated-id-" + string(rune('a'+seq)) }
	svc := &AdminService{Store: store, Sessions: store, Now: nowFn, IDGen: idFn}
	return svc, store
}

func mustCreate(t *testing.T, svc *AdminService, email string, role Role) User {
	t.Helper()
	u, err := svc.CreateUser(context.Background(), email, role, "a-strong-password")
	if err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	return u
}

func TestCreateUserValidation(t *testing.T) {
	svc, _ := adminService(t)
	ctx := context.Background()

	if _, err := svc.CreateUser(ctx, "not-an-email", RoleViewer, "a-strong-password"); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("bad email = %v", err)
	}
	if _, err := svc.CreateUser(ctx, "a@example.test", Role("SUPERUSER"), "a-strong-password"); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("bad role = %v", err)
	}
	if _, err := svc.CreateUser(ctx, "a@example.test", RoleViewer, "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("short password = %v", err)
	}
	u := mustCreate(t, svc, "a@example.test", RoleViewer)
	if u.PasswordHash != "" {
		t.Fatal("password hash leaked in the returned user")
	}
	if u.ID == "" || u.CreatedAt.IsZero() {
		t.Fatalf("user not populated: %+v", u)
	}
	if _, err := svc.CreateUser(ctx, "a@example.test", RoleViewer, "a-strong-password"); !errors.Is(err, ErrDuplicateEmail) {
		t.Fatalf("duplicate email = %v", err)
	}
}

func TestUpdateUserRoleRules(t *testing.T) {
	svc, _ := adminService(t)
	ctx := context.Background()
	admin := mustCreate(t, svc, "admin@example.test", RoleAdmin)
	op := mustCreate(t, svc, "op@example.test", RoleOperator)

	// Self-target always refused.
	if _, err := svc.UpdateUserRole(ctx, admin.ID, admin.ID, RoleOperator); !errors.Is(err, ErrSelfTarget) {
		t.Fatalf("self role change = %v", err)
	}
	// Last admin protection: admin is the only enabled ADMIN.
	if _, err := svc.UpdateUserRole(ctx, op.ID, admin.ID, RoleOperator); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin = %v", err)
	}
	// Unknown target.
	if _, err := svc.UpdateUserRole(ctx, admin.ID, "nope", RoleOperator); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("unknown target = %v", err)
	}
	// Invalid role.
	if _, err := svc.UpdateUserRole(ctx, admin.ID, op.ID, Role("BOGUS")); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("invalid role = %v", err)
	}
	// A second admin makes demoting the first legal.
	second := mustCreate(t, svc, "second-admin@example.test", RoleAdmin)
	if _, err := svc.UpdateUserRole(ctx, second.ID, admin.ID, RoleOperator); err != nil {
		t.Fatalf("demote with a second admin present: %v", err)
	}
}

func TestUpdateUserRoleRevokesSessions(t *testing.T) {
	svc, store := adminService(t)
	ctx := context.Background()
	admin := mustCreate(t, svc, "admin@example.test", RoleAdmin)
	op := mustCreate(t, svc, "op@example.test", RoleOperator)
	if err := store.CreateSession(ctx, Session{Token: "tok-op", UserID: op.ID, Role: RoleOperator, ExpiresAt: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateUserRole(ctx, admin.ID, op.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	sess, err := store.SessionByToken(ctx, "tok-op")
	if err != nil {
		t.Fatal(err)
	}
	if sess.RevokedAt.IsZero() {
		t.Fatal("role change did not revoke the target's session")
	}
}

func TestSetUserDisabledRules(t *testing.T) {
	svc, store := adminService(t)
	ctx := context.Background()
	admin := mustCreate(t, svc, "admin@example.test", RoleAdmin)
	op := mustCreate(t, svc, "op@example.test", RoleOperator)
	if err := store.CreateSession(ctx, Session{Token: "tok-op", UserID: op.ID, Role: RoleOperator, ExpiresAt: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.SetUserDisabled(ctx, admin.ID, admin.ID, true); !errors.Is(err, ErrSelfTarget) {
		t.Fatalf("self disable = %v", err)
	}
	if _, err := svc.SetUserDisabled(ctx, admin.ID, admin.ID, false); err != nil {
		t.Fatalf("self enable should be allowed (no-op-ish): %v", err)
	}
	if _, err := svc.SetUserDisabled(ctx, admin.ID, op.ID, true); err != nil {
		t.Fatal(err)
	}
	sess, err := store.SessionByToken(ctx, "tok-op")
	if err != nil {
		t.Fatal(err)
	}
	if sess.RevokedAt.IsZero() {
		t.Fatal("disable did not revoke the target's session")
	}

	// Last admin protection on disable.
	if _, err := svc.SetUserDisabled(ctx, op.ID, admin.ID, true); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last admin = %v", err)
	}
}

func TestSetUserPasswordResetRules(t *testing.T) {
	svc, store := adminService(t)
	ctx := context.Background()
	mustCreate(t, svc, "admin@example.test", RoleAdmin)
	op := mustCreate(t, svc, "op@example.test", RoleOperator)
	if err := store.CreateSession(ctx, Session{Token: "tok-op", UserID: op.ID, Role: RoleOperator, ExpiresAt: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.SetUserPassword(ctx, op.ID, "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("short reset password = %v", err)
	}
	if _, err := svc.SetUserPassword(ctx, "nope", "a-strong-password"); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("unknown target reset = %v", err)
	}
	u, err := svc.SetUserPassword(ctx, op.ID, "a-brand-new-password")
	if err != nil {
		t.Fatal(err)
	}
	if u.PasswordHash != "" {
		t.Fatal("password hash leaked in the returned user")
	}
	sess, err := store.SessionByToken(ctx, "tok-op")
	if err != nil {
		t.Fatal(err)
	}
	if sess.RevokedAt.IsZero() {
		t.Fatal("admin reset did not revoke the target's session")
	}
	stored, err := store.UserByID(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassword("a-brand-new-password", stored.PasswordHash); err != nil {
		t.Fatalf("new password does not verify: %v", err)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	svc, store := adminService(t)
	ctx := context.Background()
	op, err := svc.CreateUser(ctx, "op@example.test", RoleOperator, "the-old-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, Session{Token: "tok-1", UserID: op.ID, Role: RoleOperator, ExpiresAt: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, Session{Token: "tok-2", UserID: op.ID, Role: RoleOperator, ExpiresAt: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	if err := svc.ChangeOwnPassword(ctx, op.ID, "wrong-current", "a-brand-new-password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("wrong current password = %v", err)
	}
	if err := svc.ChangeOwnPassword(ctx, op.ID, "the-old-password", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak new password = %v", err)
	}
	if err := svc.ChangeOwnPassword(ctx, op.ID, "the-old-password", "a-brand-new-password"); err != nil {
		t.Fatal(err)
	}
	// Every session — including the one that made the change — is revoked.
	for _, tok := range []string{"tok-1", "tok-2"} {
		sess, err := store.SessionByToken(ctx, tok)
		if err != nil {
			t.Fatal(err)
		}
		if sess.RevokedAt.IsZero() {
			t.Fatalf("session %s survived a password change", tok)
		}
	}
	stored, err := store.UserByID(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassword("a-brand-new-password", stored.PasswordHash); err != nil {
		t.Fatalf("new password does not verify: %v", err)
	}
}

func TestListUsers(t *testing.T) {
	svc, _ := adminService(t)
	ctx := context.Background()
	mustCreate(t, svc, "a@example.test", RoleViewer)
	mustCreate(t, svc, "b@example.test", RoleOperator)
	users, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %d", len(users))
	}
	for _, u := range users {
		if !strings.Contains(u.Email, "@example.test") {
			t.Fatalf("unexpected user: %+v", u)
		}
	}
}
