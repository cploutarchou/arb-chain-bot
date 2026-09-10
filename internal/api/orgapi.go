package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// orgRoutes: the tenant-facing organisation surface (T-081/T-082).
//
//   - POST /me/risk-ack        acknowledge the risk disclosure (any
//     member; reachable before the gate) — compliance review #3
//   - GET  /org                the caller's organisation + members
//   - GET/POST/DELETE /org/members, POST /org/members/{id}/role
//     seat management (OWNER/ADMIN), enforced by seats.max/seats.roles
//   - POST /orgs               platform operator creates a tenant
//     organisation with an OWNER on the 14-day Operator trial
//   - PUT  /orgs/{id}/override platform operator sets the
//     entitlements_override (validated; live stays false)
func (s *Server) orgRoutes(mux *http.ServeMux) {
	needTenancy := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Tenancy == nil {
				WriteError(w, http.StatusServiceUnavailable, "tenancy_unavailable", "organisations are not available in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /api/v1/me/risk-ack", s.requireAuthPreAck(s.requireCSRF(needTenancy(s.handleRiskAck))))
	mux.HandleFunc("GET /api/v1/org", s.requireAuth(needTenancy(s.handleOrgGet)))
	mux.HandleFunc("GET /api/v1/org/members", s.requireAuth(needTenancy(s.handleOrgMembers)))
	mux.HandleFunc("POST /api/v1/org/members", s.requireAuth(s.requireOrgManager(s.requireCSRF(needTenancy(s.handleOrgMemberAdd)))))
	mux.HandleFunc("POST /api/v1/org/members/{id}/role", s.requireAuth(s.requireOrgManager(s.requireCSRF(needTenancy(s.handleOrgMemberRole)))))
	mux.HandleFunc("DELETE /api/v1/org/members/{id}", s.requireAuth(s.requireOrgManager(s.requireCSRF(needTenancy(s.handleOrgMemberRemove)))))
	mux.HandleFunc("POST /api/v1/orgs", s.requirePlatformAdmin(s.requireCSRF(needTenancy(s.handleOrgCreate))))
	mux.HandleFunc("PUT /api/v1/orgs/{id}/override", s.requirePlatformAdmin(s.requireCSRF(needTenancy(s.handleOrgOverride))))
}

func (s *Server) handleRiskAck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.Version == "" {
		WriteError(w, http.StatusBadRequest, "bad_payload", `body must be {"version": "..."}`, correlationID(r))
		return
	}
	if s.RiskAckVersion != "" && body.Version != s.RiskAckVersion {
		WriteErrorData(w, http.StatusConflict, "risk_ack_version", "acknowledge the current disclosure version", correlationID(r),
			map[string]any{"required_version": s.RiskAckVersion})
		return
	}
	p, _ := PrincipalFrom(r.Context())
	now := time.Now().UTC()
	ip := s.clientAddr(r).String()
	if err := s.Tenancy.SetRiskAck(r.Context(), p.OrgID, body.Version, now, ip); err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	s.auditWith(r, p.UserID, "org.risk_ack", "org:"+itoa(p.OrgID), map[string]any{"version": body.Version, "at": now})
	WriteData(w, http.StatusOK, map[string]any{"org_id": p.OrgID, "risk_ack_version": body.Version, "risk_ack_at": now})
}

func (s *Server) handleOrgGet(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	members, err := s.Tenancy.ListMembers(r.Context(), p.OrgID)
	if err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	WriteData(w, http.StatusOK, map[string]any{"org": meOrg(p.Org), "org_role": p.OrgRole, "members": s.rosterView(p, members), "entitlements": p.Ent()})
}

func (s *Server) handleOrgMembers(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	members, err := s.Tenancy.ListMembers(r.Context(), p.OrgID)
	if err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	WriteData(w, http.StatusOK, map[string]any{"members": s.rosterView(p, members), "seats_max": p.Ent().Seats.Max})
}

// rosterView applies S10's disclosure rule: member e-mail addresses are
// the organisation's managers' information (OWNER/ADMIN of the org, and
// the platform operator), not something every seat — including a
// VIEWER added yesterday — can enumerate.
func (s *Server) rosterView(p Principal, members []tenancy.Membership) []tenancy.Membership {
	if p.PlatformAdmin || p.OrgRole.CanManage() {
		return members
	}
	out := make([]tenancy.Membership, len(members))
	copy(out, members)
	for i := range out {
		out[i].Email = ""
	}
	return out
}

// handleOrgMemberAdd seats an EXISTING account into the organisation
// (account creation stays with the operator's console; self-service
// sign-up lands with T-085). seats.max counts every member; seats.roles
// must list the lowercase role.
//
// Audit S10: adding a user id directly is a consent problem — the added
// account learns this organisation's roster (and it learns theirs), and
// the only party who never said yes is the user. Until T-085's invited
// e-mail flow exists, the guard is: an account that already belongs to
// ANY organisation (this one included) cannot be added here. A
// first-time account is placed by the platform operator through the
// users console's org_id — an explicit, audited decision by the party
// who owns the account's creation — or waits for the invitation flow.
func (s *Server) handleOrgMemberAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.UserID == "" {
		WriteError(w, http.StatusBadRequest, "bad_payload", `body must be {"user_id": "...", "role": "OWNER|ADMIN|MEMBER|VIEWER"}`, correlationID(r))
		return
	}
	role := tenancy.Role(strings.ToUpper(body.Role))
	if !role.Valid() {
		WriteError(w, http.StatusBadRequest, "invalid_role", "role must be OWNER, ADMIN, MEMBER or VIEWER", correlationID(r))
		return
	}
	p, _ := PrincipalFrom(r.Context())
	members, err := s.Tenancy.ListMembers(r.Context(), p.OrgID)
	if err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	if s.writeEntitlementError(w, r, p.Ent().CheckSeat(len(members), seatRole(role))) {
		return
	}
	contexts, err := s.Tenancy.ContextsForUser(r.Context(), body.UserID)
	if err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	for _, c := range contexts {
		if c.Membership.OrgID == p.OrgID {
			WriteError(w, http.StatusConflict, "duplicate_member", "already a member", correlationID(r))
			return
		}
	}
	if len(contexts) > 0 {
		WriteError(w, http.StatusConflict, "user_already_member",
			"that account already belongs to an organisation; joining another requires the account's consent (operator placement at creation, or the invitation flow)", correlationID(r))
		return
	}
	if err := s.Tenancy.AddMember(r.Context(), tenancy.Membership{OrgID: p.OrgID, UserID: body.UserID, Role: role}); err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	s.auditWith(r, p.UserID, "org.member.add", "org:"+itoa(p.OrgID), map[string]any{"user_id": body.UserID, "role": role})
	WriteData(w, http.StatusCreated, map[string]any{"org_id": p.OrgID, "user_id": body.UserID, "role": role})
}

func (s *Server) handleOrgMemberRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", `body must be {"role": "..."}`, correlationID(r))
		return
	}
	role := tenancy.Role(strings.ToUpper(body.Role))
	if !role.Valid() {
		WriteError(w, http.StatusBadRequest, "invalid_role", "role must be OWNER, ADMIN, MEMBER or VIEWER", correlationID(r))
		return
	}
	p, _ := PrincipalFrom(r.Context())
	if s.writeEntitlementError(w, r, p.Ent().CheckSeatRole(seatRole(role))) {
		return
	}
	id := r.PathValue("id")
	if err := s.Tenancy.SetMemberRole(r.Context(), p.OrgID, id, role); err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	s.auditWith(r, p.UserID, "org.member.role", "org:"+itoa(p.OrgID), map[string]any{"user_id": id, "role": role})
	WriteData(w, http.StatusOK, map[string]any{"user_id": id, "role": role})
}

func (s *Server) handleOrgMemberRemove(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	id := r.PathValue("id")
	if err := s.Tenancy.RemoveMember(r.Context(), p.OrgID, id); err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	s.audit(r, p.UserID, "org.member.remove", "org:"+itoa(p.OrgID)+":user:"+id)
	// audit S3/P1-12: a removed member's API keys for THIS organisation
	// must stop working immediately rather than keep authenticating
	// until someone notices and revokes them by hand — the
	// authenticate-time membership check (resolveAPIKeyPrincipal) is
	// the durable backstop if this ever fails to run. Best-effort: the
	// membership is already gone regardless of whether this succeeds.
	if s.APIKeys != nil {
		if n, err := s.APIKeys.RevokeByMembership(r.Context(), p.OrgID, id, time.Now().UTC()); err != nil {
			s.log.Error("api key cascade revoke on membership removal failed", "org", p.OrgID, "user", id, "error", err)
		} else if n > 0 {
			s.audit(r, p.UserID, "apikey.revoke_cascade", "org:"+itoa(p.OrgID)+":user:"+id)
		}
	}
	WriteData(w, http.StatusOK, map[string]any{"status": "removed"})
}

// handleOrgCreate (platform operator): a new tenant organisation with
// its OWNER on the 14-day Operator trial (packages.md §2). No card, one
// trial per organisation by construction (trial_ends_at is set once).
func (s *Server) handleOrgCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		OwnerUserID  string `json:"owner_user_id"`
		Country      string `json:"country"`
		CustomerType string `json:"customer_type"`
		Trial        *bool  `json:"trial"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Name == "" {
		WriteError(w, http.StatusBadRequest, "bad_payload", "name is required", correlationID(r))
		return
	}
	ct := tenancy.CustomerType(body.CustomerType)
	if ct == "" {
		ct = tenancy.CustomerBusiness
	}
	if ct != tenancy.CustomerBusiness && ct != tenancy.CustomerConsumer {
		WriteError(w, http.StatusBadRequest, "invalid_customer_type", "customer_type must be consumer or business", correlationID(r))
		return
	}
	org := tenancy.Org{Name: body.Name, Country: strings.ToUpper(body.Country), CustomerType: ct, PackageCode: entitlements.PackageWatch}
	if body.Trial == nil || *body.Trial {
		end := time.Now().UTC().Add(entitlements.TrialLength)
		org.PackageCode, org.TrialEndsAt = entitlements.PackageOperator, &end
	}
	created, err := s.Tenancy.CreateOrg(r.Context(), org, body.OwnerUserID)
	if err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	p, _ := PrincipalFrom(r.Context())
	s.auditWith(r, p.UserID, "org.create", "org:"+itoa(created.ID), map[string]any{"name": created.Name, "package_code": created.PackageCode, "owner": body.OwnerUserID})
	WriteData(w, http.StatusCreated, map[string]any{"org": meOrg(created)})
}

// handleOrgOverride (platform operator): stores a pilot/grandfathering
// override after validating the MERGED document (compliance #23), so a
// document that would enable live execution is refused here, not just
// ignored at resolve time.
func (s *Server) handleOrgOverride(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		WriteError(w, http.StatusBadRequest, "bad_id", "organisation id must be numeric", correlationID(r))
		return
	}
	raw, err := readLimited(r, 64<<10)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "could not read body", correlationID(r))
		return
	}
	org, err := s.Tenancy.Org(r.Context(), id)
	if err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	var override []byte
	if strings.TrimSpace(string(raw)) != "" && strings.TrimSpace(string(raw)) != "null" {
		base, ok := entitlements.Package(org.PackageCode)
		if !ok {
			WriteError(w, http.StatusConflict, "unknown_package", "organisation package is not a known package", correlationID(r))
			return
		}
		if _, err := entitlements.Merge(base, raw); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_override", err.Error(), correlationID(r))
			return
		}
		override = raw
	}
	if err := s.Tenancy.SetOverride(r.Context(), id, override); err != nil {
		s.writeTenancyError(w, r, err)
		return
	}
	if s.Entitlements != nil {
		s.Entitlements.Invalidate(id)
	}
	p, _ := PrincipalFrom(r.Context())
	s.auditWith(r, p.UserID, "org.override", "org:"+itoa(id), json.RawMessage(orDefault(override, []byte("null"))))
	WriteData(w, http.StatusOK, map[string]any{"org_id": id, "override": json.RawMessage(orDefault(override, []byte("null")))})
}

func (s *Server) writeTenancyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tenancy.ErrUnknownOrg), errors.Is(err, tenancy.ErrNoMembership), errors.Is(err, auth.ErrUnknownUser):
		WriteError(w, http.StatusNotFound, "not_found", "no such organisation or member", correlationID(r))
	case errors.Is(err, tenancy.ErrDuplicate):
		WriteError(w, http.StatusConflict, "duplicate_member", "already a member", correlationID(r))
	case errors.Is(err, tenancy.ErrLastOwner):
		WriteError(w, http.StatusConflict, "last_owner", "an organisation keeps at least one owner", correlationID(r))
	case errors.Is(err, tenancy.ErrInvalidRole):
		WriteError(w, http.StatusBadRequest, "invalid_role", err.Error(), correlationID(r))
	default:
		s.log.Error("organisation change failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "org_failed", "organisation change failed", correlationID(r))
	}
}

// seatRole maps the membership role onto the schema's lowercase
// seats.roles vocabulary (MEMBER is "operator" in packages.md terms).
func seatRole(r tenancy.Role) string {
	switch r {
	case tenancy.RoleOwner:
		return "owner"
	case tenancy.RoleAdmin:
		return "admin"
	case tenancy.RoleMember:
		return "operator"
	default:
		return "viewer"
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func parseID(s string) (int64, bool) {
	if s == "" || len(s) > 18 {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, n > 0
}

func orDefault(b, def []byte) []byte {
	if len(b) == 0 {
		return def
	}
	return b
}
