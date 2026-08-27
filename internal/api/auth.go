package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"regexp"
	"strings"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

const sessionCookie = "arb_session"

type ctxKey int

const principalKey ctxKey = 1

// Principal is the authenticated caller attached to the request context:
// the account, its console RBAC role, and — since T-081 — the
// organisation it acts in, its membership role, the platform_admin flag
// and the organisation's resolved entitlements.
type Principal struct {
	UserID string
	Role   auth.Role
	// PlatformAdmin is users.platform_admin: the operator's own staff.
	// It gates the exchange-credential vault group and the system
	// routes (compliance review 2026-08-27 #1). Never derived from a
	// package or a membership role.
	PlatformAdmin bool
	OrgID         int64
	OrgRole       tenancy.Role
	Org           tenancy.Org
	// Entitlements is the organisation's effective document
	// (packages.md §3); nil only when the resolver is not wired, in
	// which case the package document is used directly.
	Entitlements *entitlements.Entitlements
	// Suspended marks a seat suspended after a downgrade (packages.md
	// §3.2 seats.max); every gated route refuses it.
	Suspended bool
}

// Ent returns the effective entitlements (never nil: falls back to the
// organisation's package document, then Watch).
func (p Principal) Ent() entitlements.Entitlements {
	if p.Entitlements != nil {
		return *p.Entitlements
	}
	code := p.Org.PackageCode
	if p.OrgID == tenancy.PlatformOrgID && code == "" {
		code = entitlements.PackageInstitution
	}
	if doc, ok := entitlements.Package(code); ok {
		return doc
	}
	doc, _ := entitlements.Package(entitlements.PackageWatch)
	return doc
}

// PrincipalFrom extracts the caller (ok=false on unauthenticated paths).
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// csrfFor derives the per-session CSRF token as HMAC(server key, session
// token): stateless double-submit — the browser gets it at login and
// echoes it in X-CSRF-Token; the server recomputes from the cookie. The
// key is process-random (sessions are invalidated on restart until the
// storage layer persists them, so this loses nothing today).
func (s *Server) csrfFor(sessionToken string) string {
	mac := hmac.New(sha256.New, s.csrfKey)
	mac.Write([]byte(sessionToken))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func newCSRFKey() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic("api: csrf key: " + err.Error())
	}
	return k
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Role      string `json:"role"`
	CSRFToken string `json:"csrf_token"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil {
		WriteError(w, http.StatusServiceUnavailable, "auth_unconfigured", "no users configured", correlationID(r))
		return
	}
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "malformed login payload", correlationID(r))
		return
	}
	sess, err := s.Auth.Login(r.Context(), req.Email, req.Password, clientAddr(r))
	if err != nil {
		status, code := http.StatusUnauthorized, "invalid_credentials"
		if errors.Is(err, auth.ErrThrottled) {
			status, code = http.StatusTooManyRequests, "throttled"
		}
		// Uniform message: no user-existence oracle.
		WriteError(w, status, code, "login failed", correlationID(r))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.ExpiresAt,
	})
	s.audit(r, sess.UserID, "auth.login", "user:"+sess.UserID)
	WriteData(w, http.StatusOK, loginResponse{Role: string(sess.Role), CSRFToken: s.csrfFor(sess.Token)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.Auth.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	if p, ok := PrincipalFrom(r.Context()); ok {
		s.audit(r, p.UserID, "auth.logout", "user:"+p.UserID)
	}
	WriteData(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

// handleMe serves GET /api/v1/auth/me and GET /api/v1/me: the account,
// its console role, the organisation and membership role, whether the
// risk disclosure still has to be acknowledged, and the organisation's
// effective entitlements (packages.md §3: the console only READS them;
// every limit is enforced server-side).
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	ent := p.Ent()
	out := map[string]any{
		"user_id":           p.UserID,
		"role":              string(p.Role),
		"platform_admin":    p.PlatformAdmin,
		"org":               meOrg(p.Org),
		"org_role":          string(p.OrgRole),
		"risk_ack_required": s.riskAckRequired(p),
		"risk_ack_version":  s.RiskAckVersion,
		"entitlements":      ent,
	}
	// Re-derive the CSRF token so a reloaded console recovers it without
	// re-authenticating (it is bound to the session, not stored).
	if c, err := r.Cookie(sessionCookie); err == nil {
		out["csrf_token"] = s.csrfFor(c.Value)
	}
	WriteData(w, http.StatusOK, out)
}

// meOrg is the organisation view on /me (no override document, no IP).
func meOrg(o tenancy.Org) map[string]any {
	return map[string]any{
		"id": o.ID, "name": o.Name, "package_code": o.PackageCode, "country": o.Country,
		"customer_type": o.CustomerType, "risk_ack_version": o.RiskAckVersion, "risk_ack_at": o.RiskAckAt,
		"trial_ends_at": o.TrialEndsAt, "created_at": o.CreatedAt,
	}
}

// riskAckRequired: compliance review #3/#11 — every organisation must
// have acknowledged the current risk disclosure version before it can
// use the product. The platform organisation (the operator's own
// staff, whether or not platform_admin) is exempt: they are not
// clients. An empty RiskAckVersion disables the gate (profiles that
// predate tenancy).
func (s *Server) riskAckRequired(p Principal) bool {
	if s.RiskAckVersion == "" || p.PlatformAdmin || p.OrgID == tenancy.PlatformOrgID {
		return false
	}
	return p.Org.RiskAckVersion != s.RiskAckVersion
}

// requireAuth validates the session cookie, attaches the principal with
// its organisation and entitlements, and refuses (403
// risk_ack_required) until the organisation has acknowledged the risk
// disclosure. requireAuthPreAck is the same without the acknowledgement
// gate, for the handful of routes a not-yet-acknowledged user needs
// (/me, /me/risk-ack, logout, own password).
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.authenticate(true, next)
}

func (s *Server) requireAuthPreAck(next http.HandlerFunc) http.HandlerFunc {
	return s.authenticate(false, next)
}

func (s *Server) authenticate(gateAck bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Auth == nil {
			WriteError(w, http.StatusServiceUnavailable, "auth_unconfigured", "no users configured", correlationID(r))
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "unauthenticated", "session required", correlationID(r))
			return
		}
		sess, err := s.Auth.Validate(r.Context(), c.Value)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "unauthenticated", "session invalid", correlationID(r))
			return
		}
		principal, err := s.resolvePrincipal(r.Context(), sess)
		if err != nil {
			if errors.Is(err, tenancy.ErrNoMembership) {
				WriteError(w, http.StatusForbidden, "no_organisation", "this account belongs to no organisation", correlationID(r))
				return
			}
			s.log.Error("principal resolution failed", "user", sess.UserID, "error", err)
			WriteError(w, http.StatusInternalServerError, "tenancy_failed", "resolving the organisation failed", correlationID(r))
			return
		}
		if principal.Suspended {
			WriteError(w, http.StatusForbidden, "membership_suspended", "your seat is suspended; ask an organisation owner", correlationID(r))
			return
		}
		if gateAck && s.riskAckRequired(principal) {
			WriteErrorData(w, http.StatusForbidden, "risk_ack_required",
				"acknowledge the current risk disclosure before using the platform", correlationID(r),
				map[string]any{"required_version": s.RiskAckVersion})
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, principal)
		ctx = tenancy.WithOrg(ctx, principal.OrgID)
		next(w, r.WithContext(ctx))
	}
}

// resolvePrincipal attaches organisation, membership role, platform
// flag and entitlements. Without a tenancy store (database-less
// profiles) every account acts in the platform organisation and the
// legacy console role stands in for the membership role, with ADMIN
// treated as platform admin so the operator's console keeps working.
func (s *Server) resolvePrincipal(ctx context.Context, sess auth.Session) (Principal, error) {
	p := Principal{UserID: sess.UserID, Role: sess.Role, PlatformAdmin: sess.PlatformAdmin}
	if s.Tenancy == nil {
		p.OrgID = tenancy.PlatformOrgID
		p.Org = tenancy.Org{ID: tenancy.PlatformOrgID, Name: "platform", PackageCode: entitlements.PackageInstitution, CustomerType: tenancy.CustomerBusiness}
		p.OrgRole = platformRoleFor(sess.Role)
		p.PlatformAdmin = p.PlatformAdmin || sess.Role == auth.RoleAdmin
		return p, nil
	}
	tc, err := s.Tenancy.ContextForUser(ctx, sess.UserID)
	if err != nil {
		return Principal{}, err
	}
	p.OrgID, p.Org, p.OrgRole, p.Suspended = tc.Org.ID, tc.Org, tc.Membership.Role, tc.Membership.Suspended
	if s.Entitlements != nil {
		doc, err := s.Entitlements.For(ctx, p.OrgID)
		if err != nil {
			return Principal{}, err
		}
		p.Entitlements = &doc
	}
	return p, nil
}

func platformRoleFor(r auth.Role) tenancy.Role {
	switch r {
	case auth.RoleAdmin:
		return tenancy.RoleOwner
	case auth.RoleOperator:
		return tenancy.RoleAdmin
	default:
		return tenancy.RoleViewer
	}
}

// requirePlatformAdmin gates a route on users.platform_admin: the
// exchange-credential vault group, user management, platform settings,
// engine restarts, billing price mapping. Tenant OWNER/ADMIN get 403
// regardless of package (compliance review #1).
func (s *Server) requirePlatformAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		if !p.PlatformAdmin {
			WriteError(w, http.StatusForbidden, "platform_admin_required", "this route is reserved for the platform operator", correlationID(r))
			return
		}
		next(w, r)
	})
}

// requireOrgRole gates a route on the caller's membership role inside
// its organisation (OWNER/ADMIN manage members, billing, the risk
// acknowledgement). Platform admins pass.
func (s *Server) requireOrgManager(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		if !p.PlatformAdmin && !p.OrgRole.CanManage() {
			WriteError(w, http.StatusForbidden, "org_role", "organisation owner or admin required", correlationID(r))
			return
		}
		next(w, r)
	}
}

// writeEntitlementError maps entitlements.Exceeded to 403
// entitlement_exceeded with the breached key and limit in data
// (packages.md §3.2); any other error is a 500.
func (s *Server) writeEntitlementError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	var ex *entitlements.Exceeded
	if errors.As(err, &ex) {
		WriteErrorData(w, http.StatusForbidden, "entitlement_exceeded", ex.Message, correlationID(r),
			map[string]any{"key": ex.Key, "limit": ex.Limit})
		return true
	}
	s.log.Error("entitlement check failed", "error", err)
	WriteError(w, http.StatusInternalServerError, "entitlement_failed", "entitlement check failed", correlationID(r))
	return true
}

// requireCSRF enforces the double-submit token on mutating requests.
func (s *Server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "unauthenticated", "session required", correlationID(r))
			return
		}
		if auth.VerifyCSRF(s.csrfFor(c.Value), r.Header.Get("X-CSRF-Token")) != nil {
			WriteError(w, http.StatusForbidden, "csrf", "missing or invalid CSRF token", correlationID(r))
			return
		}
		next(w, r)
	}
}

// requirePerm gates a handler on an RBAC permission — the backend gate,
// not the UI (docs/security.md §5).
func (s *Server) requirePerm(p auth.Permission, next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		principal, _ := PrincipalFrom(r.Context())
		if !auth.Can(principal.Role, p) {
			WriteError(w, http.StatusForbidden, "forbidden", "insufficient role", correlationID(r))
			return
		}
		next(w, r)
	})
}

// correlationIDPattern bounds what a client-supplied correlation ID may
// look like before it is echoed into responses, logs, and audit rows
// (audit S-017): short, printable, no whitespace or control characters.
var correlationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func correlationID(r *http.Request) string {
	id := r.Header.Get("X-Correlation-ID")
	if id == "" || correlationIDPattern.MatchString(id) {
		return id
	}
	return "invalid-correlation-id"
}

func clientAddr(r *http.Request) netip.Addr {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	if a, err := netip.ParseAddr(host); err == nil {
		return a
	}
	return netip.IPv4Unspecified()
}

// requireOnlyPlatformAdmin is requirePlatformAdmin for handlers that
// are already behind requireAuth/requirePerm (no second session
// lookup).
func (s *Server) requireOnlyPlatformAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		if !p.PlatformAdmin {
			WriteError(w, http.StatusForbidden, "platform_admin_required", "this route is reserved for the platform operator", correlationID(r))
			return
		}
		next(w, r)
	}
}
