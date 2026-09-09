package app

import (
	"context"
	"net/netip"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
)

// Acceptance (audit S6/P1-14): the bootstrap admin must be created only
// when the configured email is not already registered, and must never
// touch an existing row — a demoted, disabled or password-rotated
// account must survive every subsequent restart unchanged.

func TestBootstrapAdminCreatesWhenAbsent(t *testing.T) {
	mem := auth.NewMemoryStore()
	cfg := config.Bootstrap{AdminEmail: "admin@example.test", AdminPassword: "a-strong-bootstrap-pw"}
	bootstrapAdmin(cfg, testLogger(), mem)

	u, err := mem.UserByEmail(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatalf("bootstrap admin not created: %v", err)
	}
	if u.Role != auth.RoleAdmin || u.Disabled {
		t.Fatalf("bootstrap admin = %+v, want an enabled ADMIN", u)
	}
	if err := auth.VerifyPassword("a-strong-bootstrap-pw", u.PasswordHash); err != nil {
		t.Fatalf("bootstrap password does not verify: %v", err)
	}
}

func TestBootstrapAdminNeverOverwritesExistingAccount(t *testing.T) {
	mem := auth.NewMemoryStore()
	originalHash, err := auth.HashPassword("the-original-password")
	if err != nil {
		t.Fatal(err)
	}
	// Simulates an account the console later demoted and disabled (or
	// simply gave a new password) after an earlier boot created it.
	mem.AddUser(auth.User{
		ID: "admin-bootstrap", Email: "admin@example.test",
		PasswordHash: originalHash, Role: auth.RoleViewer, Disabled: true,
	})

	cfg := config.Bootstrap{AdminEmail: "admin@example.test", AdminPassword: "a-different-env-password"}
	bootstrapAdmin(cfg, testLogger(), mem)

	got, err := mem.UserByEmail(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != auth.RoleViewer {
		t.Fatalf("role changed by bootstrap: got %s, want VIEWER (untouched)", got.Role)
	}
	if !got.Disabled {
		t.Fatal("disabled account was silently re-enabled by bootstrap")
	}
	if err := auth.VerifyPassword("the-original-password", got.PasswordHash); err != nil {
		t.Fatalf("original password no longer verifies: %v", err)
	}
	if err := auth.VerifyPassword("a-different-env-password", got.PasswordHash); err == nil {
		t.Fatal("bootstrap overwrote the password with the env value")
	}
}

func TestBootstrapAdminSkippedWhenUnconfigured(t *testing.T) {
	mem := auth.NewMemoryStore()
	bootstrapAdmin(config.Bootstrap{}, testLogger(), mem)
	users, err := mem.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Fatalf("no admin should have been created, got %+v", users)
	}
}

// TestBuildAuthBootstrapsWorkingAdmin locks in the happy path of the
// buildAuth refactor: a fresh (no-database) profile with the bootstrap
// env vars set still produces a manager that can log the admin in.
func TestBuildAuthBootstrapsWorkingAdmin(t *testing.T) {
	cfg := config.Bootstrap{AdminEmail: "admin@example.test", AdminPassword: "a-strong-bootstrap-pw"}
	mgr, adminSvc := buildAuth(cfg, testLogger(), nil)
	if mgr == nil || adminSvc == nil {
		t.Fatal("buildAuth returned nil")
	}
	sess, err := mgr.Login(context.Background(), "admin@example.test", "a-strong-bootstrap-pw", netip.MustParseAddr("203.0.113.7"))
	if err != nil {
		t.Fatalf("bootstrap admin login failed: %v", err)
	}
	if sess.Role != auth.RoleAdmin {
		t.Fatalf("session role = %s, want ADMIN", sess.Role)
	}
}
