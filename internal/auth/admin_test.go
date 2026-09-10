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

// fakeAPIKeyRevoker records RevokeByOwner calls for the cascade tests
// below (audit S3/P1-12); it never touches real storage.
type fakeAPIKeyRevoker struct {
	revokedFor []string
	revoked    int
	err        error
}

func (f *fakeAPIKeyRevoker) RevokeByOwner(_ context.Context, userID string, _ time.Time) (int, error) {
	f.revokedFor = append(f.revokedFor, userID)
	return f.revoked, f.err
}

// TestSetUserDisabledCascadesAPIKeyRevoke is the audit S3/P1-12
// regression: disabling an account must revoke every API key it
// minted and record that cascade as its own audit event, distinct
// from the caller's own "user.disable" row.
func TestSetUserDisabledCascadesAPIKeyRevoke(t *testing.T) {
	svc, _ := adminService(t)
	ctx := context.Background()
	admin := mustCreate(t, svc, "admin@example.test", RoleAdmin)
	op := mustCreate(t, svc, "op@example.test", RoleOperator)

	revoker := &fakeAPIKeyRevoker{revoked: 2}
	svc.APIKeys = revoker
	var audited []string
	svc.AuditCascade = func(actor, action, entity string) {
		audited = append(audited, actor+"|"+action+"|"+entity)
	}

	if _, err := svc.SetUserDisabled(ctx, admin.ID, op.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(revoker.revokedFor) != 1 || revoker.revokedFor[0] != op.ID {
		t.Fatalf("RevokeByOwner not called for the disabled account: %+v", revoker.revokedFor)
	}
	want := admin.ID + "|apikey.revoke_cascade|user:" + op.ID
	if len(audited) != 1 || audited[0] != want {
		t.Fatalf("cascade audit = %+v, want [%q]", audited, want)
	}
}

// Re-enabling an account must never touch its API keys — only a
// disable is a security event serious enough to revoke a bearer
// credential.
func TestSetUserEnabledDoesNotRevokeAPIKeys(t *testing.T) {
	svc, _ := adminService(t)
	ctx := context.Background()
	admin := mustCreate(t, svc, "admin@example.test", RoleAdmin)
	op := mustCreate(t, svc, "op@example.test", RoleOperator)
	revoker := &fakeAPIKeyRevoker{revoked: 1}
	svc.APIKeys = revoker

	if _, err := svc.SetUserDisabled(ctx, admin.ID, op.ID, false); err != nil {
		t.Fatal(err)
	}
	if len(revoker.revokedFor) != 0 {
		t.Fatalf("enabling a user must not cascade-revoke API keys: %+v", revoker.revokedFor)
	}
}

// When RevokeByOwner finds nothing to revoke, no cascade audit event
// is recorded — a no-op must not manufacture a history entry.
func TestSetUserDisabledNoCascadeAuditWhenNoKeysRevoked(t *testing.T) {
	svc, _ := adminService(t)
	ctx := context.Background()
	admin := mustCreate(t, svc, "admin@example.test", RoleAdmin)
	op := mustCreate(t, svc, "op@example.test", RoleOperator)
	svc.APIKeys = &fakeAPIKeyRevoker{revoked: 0}
	audited := 0
	svc.AuditCascade = func(string, string, string) { audited++ }

	if _, err := svc.SetUserDisabled(ctx, admin.ID, op.ID, true); err != nil {
		t.Fatal(err)
	}
	if audited != 0 {
		t.Fatalf("cascade audit fired with nothing revoked: %d calls", audited)
	}
}

// TestChangeOwnPasswordThrottled is the P3-13 regression test: repeated
// wrong-current-password attempts against ChangeOwnPassword must lock
// out, the same as Manager.Login already does for unauthenticated
// attempts.
func TestChangeOwnPasswordThrottled(t *testing.T) {
	svc, _ := adminService(t)
	svc.PasswordThrottle = NewThrottle(3, time.Minute, time.Hour)
	ctx := context.Background()
	u := mustCreate(t, svc, "throttled@example.test", RoleViewer)

	for i := 0; i < 3; i++ {
		if err := svc.ChangeOwnPassword(ctx, u.ID, "wrong-password", "a-brand-new-password"); !errors.Is(err, ErrPasswordMismatch) {
			t.Fatalf("attempt %d: got %v, want ErrPasswordMismatch", i, err)
		}
	}
	if err := svc.ChangeOwnPassword(ctx, u.ID, "a-strong-password", "a-brand-new-password"); !errors.Is(err, ErrThrottled) {
		t.Fatalf("after 3 failures: got %v, want ErrThrottled (even with the CORRECT password)", err)
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
		// P3-6: ListUsers must never hand back a password hash, even
		// though MemoryStore itself stores one per user.
		if u.PasswordHash != "" {
			t.Fatalf("password hash leaked from ListUsers: %+v", u)
		}
	}
}

// TestCreateUserEmailCaseNormalized is the P3-7 regression test:
// "Alice@Example.test" and "alice@example.test" must collide as the
// same account.
func TestCreateUserEmailCaseNormalized(t *testing.T) {
	svc, store := adminService(t)
	ctx := context.Background()
	u := mustCreate(t, svc, "  Alice@Example.test  ", RoleViewer)
	if u.Email != "alice@example.test" {
		t.Fatalf("email not normalized: %q", u.Email)
	}
	if _, err := svc.CreateUser(ctx, "alice@example.test", RoleViewer, "a-strong-password"); !errors.Is(err, ErrDuplicateEmail) {
		t.Fatalf("differently-cased duplicate = %v, want ErrDuplicateEmail", err)
	}
	if _, err := store.UserByEmail(ctx, "alice@example.test"); err != nil {
		t.Fatalf("stored under the normalized key: %v", err)
	}
}

// TestUpdateUserRoleLastAdminRaceIsAtomic is the P2-5 regression test:
// two admins demoted CONCURRENTLY must not both succeed and leave zero
// enabled admins. Before the fix, AdminService did list-then-check
// (requireNotLastAdmin) as a round trip separate from the store write,
// so two goroutines could each observe "the other one is still an
// admin" and both proceed. Run with -race.
func TestUpdateUserRoleLastAdminRaceIsAtomic(t *testing.T) {
	svc, _ := adminService(t)
	ctx := context.Background()
	a := mustCreate(t, svc, "a@example.test", RoleAdmin)
	b := mustCreate(t, svc, "b@example.test", RoleAdmin)
	bystander := mustCreate(t, svc, "bystander@example.test", RoleViewer)

	start := make(chan struct{})
	results := make(chan error, 2)
	demote := func(targetID string) {
		<-start
		_, err := svc.UpdateUserRole(ctx, bystander.ID, targetID, RoleOperator)
		results <- err
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
		case errors.Is(err, ErrLastAdmin):
			lastAdminRefusals++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || lastAdminRefusals != 1 {
		t.Fatalf("concurrent demotions: successes=%d refusals=%d (want exactly one of each — both succeeding would leave zero admins)",
			successes, lastAdminRefusals)
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admins := 0
	for _, u := range users {
		if u.Role == RoleAdmin && !u.Disabled {
			admins++
		}
	}
	if admins != 1 {
		t.Fatalf("enabled admins after the race = %d, want 1", admins)
	}
}

// CreateUserInOrg validation (audit S4 follow-up): the org-placement
// variant refuses an impossible org id and an OWNER membership (owners
// are granted by CreateOrg behind the last-owner guard), defaults the
// role to MEMBER, and never touches the platform join when one is set.
// The join itself is honoured by the pgx store inside its transaction
// (see internal/storage); the memory store has no memberships to write.
func TestCreateUserInOrgValidation(t *testing.T) {
	svc, _ := adminService(t)
	ctx := context.Background()

	if _, err := svc.CreateUserInOrg(ctx, "a@x.test", RoleOperator, "long-enough-password", 0, "MEMBER"); err == nil {
		t.Fatal("org id 0 accepted (must be positive)")
	}
	if _, err := svc.CreateUserInOrg(ctx, "a@x.test", RoleOperator, "long-enough-password", 7, "OWNER"); err == nil {
		t.Fatal("OWNER membership accepted")
	}
	if _, err := svc.CreateUserInOrg(ctx, "a@x.test", RoleOperator, "long-enough-password", 7, "SUPREME-RULER"); err == nil {
		t.Fatal("invalid membership role accepted")
	}
	u, err := svc.CreateUserInOrg(ctx, "tenant-op@x.test", RoleOperator, "long-enough-password", 7, "")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "tenant-op@x.test" || u.Role != RoleOperator {
		t.Fatalf("user = %+v", u)
	}
}
