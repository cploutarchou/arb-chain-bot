package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// Acceptance (audit S3/P1-12): disabling the owner of a live API key
// must refuse the key on the very next request, not merely at some
// later revocation sweep.
func TestAPIKeyRefusedAfterOwnerDisabled(t *testing.T) {
	s, mux, _, _ := newAPIKeyServer(t)
	deskCookie, deskCSRF := login(t, mux, "owner@desk.test", "desk-owner-pw")
	token, _, createRec := createAPIKey(t, mux, deskCookie, deskCSRF, "ci", []string{"read"})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", createRec.Code, createRec.Body.String())
	}
	if rec := bearerCall(t, mux, http.MethodGet, "/api/v1/screener/rules", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("bearer read before disable = %d: %s", rec.Code, rec.Body.String())
	}

	// u-admin (platform staff, unrelated to the desk org) disables the
	// key's owner — plenty of other enabled admins exist in this
	// fixture, so the last-admin guard does not block it.
	if _, err := s.Users.SetUserDisabled(context.Background(), "u-admin", "desk-owner", true); err != nil {
		t.Fatalf("disable owner: %v", err)
	}

	rec := bearerCall(t, mux, http.MethodGet, "/api/v1/screener/rules", token, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bearer call after owner disabled = %d: %s", rec.Code, rec.Body.String())
	}
}

// Acceptance (audit S3/P1-12): removing the key owner's membership in
// the key's organisation must refuse the key on the very next request
// — both through the authenticate-time membership check and through
// the cascade revoke handleOrgMemberRemove performs.
func TestAPIKeyRefusedAfterMembershipRemoved(t *testing.T) {
	_, mux, ts, _ := newAPIKeyServer(t)
	ctx := context.Background()
	deskCookie, deskCSRF := login(t, mux, "owner@desk.test", "desk-owner-pw")
	token, _, createRec := createAPIKey(t, mux, deskCookie, deskCSRF, "ci", []string{"read"})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", createRec.Code, createRec.Body.String())
	}
	if rec := bearerCall(t, mux, http.MethodGet, "/api/v1/screener/rules", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("bearer read before removal = %d: %s", rec.Code, rec.Body.String())
	}

	tc, err := ts.ContextForUser(ctx, "desk-owner")
	if err != nil {
		t.Fatal(err)
	}
	orgID := tc.Org.ID
	// A second owner, so removing desk-owner does not trip the
	// last-owner guard.
	if err := ts.SetMemberRole(ctx, orgID, "desk-viewer", tenancy.RoleOwner); err != nil {
		t.Fatal(err)
	}
	viewerCookie, viewerCSRF := login(t, mux, "viewer@desk.test", "desk-viewer-pw")
	removeRec := call(t, mux, http.MethodDelete, "/api/v1/org/members/desk-owner", viewerCookie, viewerCSRF, nil)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("remove member = %d: %s", removeRec.Code, removeRec.Body.String())
	}

	rec := bearerCall(t, mux, http.MethodGet, "/api/v1/screener/rules", token, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bearer call after membership removed = %d: %s", rec.Code, rec.Body.String())
	}
}
