package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/apikey"
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
	// APIKey is set only when the request authenticated with
	// Authorization: Bearer <key> (T-086) rather than the session
	// cookie. Its presence is the one signal every scope/CSRF/RBAC
	// special-case keys off; it is never both set and unset for the
	// same request, and it is never forged into a session-authenticated
	// Principal.
	APIKey *apikey.Key
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
		if token, ok := bearerToken(r); ok {
			s.authenticateAPIKey(gateAck, token, next)(w, r)
			return
		}
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

// bearerToken extracts the plaintext key from "Authorization: Bearer
// <token>", ok=false for any other (or absent) header so the caller
// falls back to the session-cookie path.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	return tok, tok != ""
}

// authenticateAPIKey is the Bearer counterpart of authenticate(): it
// resolves the organisation and entitlements exactly like a session
// (resolveAPIKeyPrincipal), then applies the two checks that have no
// session equivalent — api.enabled/api.scopes (packages.md §3.2 "api.*"
// -> 403 entitlement_exceeded) and the per-key token bucket (429 +
// Retry-After) — in that order, so a Watch/Signal organisation without
// API access gets the entitlement error rather than a confusing rate
// limit on a bucket it was never granted.
func (s *Server) authenticateAPIKey(gateAck bool, token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.APIKeys == nil {
			WriteError(w, http.StatusServiceUnavailable, "auth_unconfigured", "API keys are not available in this profile", correlationID(r))
			return
		}
		key, ok, err := apikey.Authenticate(r.Context(), s.APIKeys, token)
		if err != nil {
			s.log.Error("api key authentication failed", "error", err)
			WriteError(w, http.StatusInternalServerError, "api_key_failed", "authentication failed", correlationID(r))
			return
		}
		if !ok {
			WriteError(w, http.StatusUnauthorized, "unauthenticated", "invalid or revoked API key", correlationID(r))
			return
		}
		principal, err := s.resolveAPIKeyPrincipal(r.Context(), key)
		if err != nil {
			if errors.Is(err, tenancy.ErrUnknownOrg) {
				WriteError(w, http.StatusUnauthorized, "unauthenticated", "invalid API key", correlationID(r))
				return
			}
			s.log.Error("api key principal resolution failed", "key_id", key.ID, "error", err)
			WriteError(w, http.StatusInternalServerError, "tenancy_failed", "resolving the organisation failed", correlationID(r))
			return
		}
		ent := principal.Ent()
		if s.writeEntitlementError(w, r, ent.CheckAPIScope("read")) {
			return
		}
		if s.APIRateLimiter != nil {
			allowed, retryAfter := s.APIRateLimiter.Allow(key.ID, ent.API.RatePerMin, ent.API.Burst, time.Now())
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
				WriteError(w, http.StatusTooManyRequests, "rate_limited", "API rate limit exceeded", correlationID(r))
				return
			}
		}
		if gateAck && s.riskAckRequired(principal) {
			WriteErrorData(w, http.StatusForbidden, "risk_ack_required",
				"the organisation must acknowledge the current risk disclosure before using the API", correlationID(r),
				map[string]any{"required_version": s.RiskAckVersion})
			return
		}
		s.touchAPIKey(key.ID)
		ctx := context.WithValue(r.Context(), principalKey, principal)
		ctx = tenancy.WithOrg(ctx, principal.OrgID)
		next(w, r.WithContext(ctx))
	}
}

// resolveAPIKeyPrincipal builds a Principal for a Bearer caller. Role is
// always the lowest console role (VIEWER): API-key authorization runs
// entirely on api.scopes (requirePerm and requireAPIScope special-case
// p.APIKey), never on the auth.Role RBAC matrix built for human staff.
// PlatformAdmin is always false, unconditionally — an API key minted by
// staff who happen to hold platform_admin must never reach the
// exchange-credential vault group or any platform_admin-only route.
func (s *Server) resolveAPIKeyPrincipal(ctx context.Context, key apikey.Key) (Principal, error) {
	p := Principal{UserID: key.UserID, Role: auth.RoleViewer, PlatformAdmin: false, APIKey: &key}
	if s.Tenancy == nil {
		return Principal{}, tenancy.ErrUnknownOrg
	}
	org, err := s.Tenancy.Org(ctx, key.OrgID)
	if err != nil {
		return Principal{}, err
	}
	p.OrgID, p.Org, p.OrgRole = org.ID, org, tenancy.RoleViewer
	if s.Entitlements != nil {
		doc, err := s.Entitlements.For(ctx, p.OrgID)
		if err != nil {
			return Principal{}, err
		}
		p.Entitlements = &doc
	}
	return p, nil
}

// touchAPIKey persists last_used_at at most once per apiKeyTouchInterval
// per key: a synchronous UPDATE on every authenticated request would be
// pure write amplification on the hot path.
func (s *Server) touchAPIKey(id string) {
	now := time.Now()
	if v, ok := s.apiKeyTouch.Load(id); ok {
		if now.Sub(v.(time.Time)) < apiKeyTouchInterval {
			return
		}
	}
	s.apiKeyTouch.Store(id, now)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.APIKeys.Touch(ctx, id, now); err != nil {
			s.log.Warn("api key last_used_at update failed", "key_id", id, "error", err)
		}
	}()
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
// Bearer-authenticated (API key) callers are exempt: CSRF defends
// against a browser being tricked into replaying an ambient cookie, and
// a Bearer token is never sent ambiently by a browser — there is no
// cross-site forgery surface to defend (requireCSRF must run after
// requireAuth so PrincipalFrom is populated; every call site in this
// package already nests it inside requireAuth/requirePerm).
func (s *Server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p, ok := PrincipalFrom(r.Context()); ok && p.APIKey != nil {
			next(w, r)
			return
		}
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
// not the UI (docs/security.md §5). An API-key principal never consults
// the auth.Role matrix (it is always RoleViewer and must not silently
// gain it); apiKeyCanPermission is the one place that decides which
// permissions a Bearer caller may ever reach, keyed off api.scopes.
func (s *Server) requirePerm(p auth.Permission, next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		principal, _ := PrincipalFrom(r.Context())
		allowed := auth.Can(principal.Role, p)
		if principal.APIKey != nil {
			allowed = apiKeyCanPermission(principal, p)
		}
		if !allowed {
			WriteError(w, http.StatusForbidden, "forbidden", "insufficient role", correlationID(r))
			return
		}
		next(w, r)
	})
}

// apiKeyCanPermission is the coarse permission gate for Bearer callers
// (T-086): only the two screener permissions are reachable at all, and
// each requires BOTH layers to agree — the organisation's package must
// include the scope (ent.CheckAPIScope, packages.md §3.2 api.*) AND the
// key itself must have been granted it (hasScope: a key minted with
// only "read" must never reach a write route just because its
// organisation's package would allow a differently-scoped key to).
// Every other permission — user management, exchange config, risk
// config, system config, paper control, platform settings — is refused
// outright, regardless of what auth.Role the matrix would otherwise
// grant a human with the same scopes. Handlers that need the EXACT
// scope for their own mutation (rules:write vs templates:write) narrow
// further with requireAPIScope; this function only decides whether the
// route family is reachable by an API key at all.
func apiKeyCanPermission(p Principal, perm auth.Permission) bool {
	ent := p.Ent()
	keyHasScope := func(scope string) bool {
		return ent.CheckAPIScope(scope) == nil && hasScope(p.APIKey.Scopes, scope)
	}
	switch perm {
	case auth.PermScreenerView:
		return keyHasScope("read")
	case auth.PermScreenerConfig:
		return keyHasScope("rules:write") || keyHasScope("templates:write") || keyHasScope("paper:write")
	default:
		return false
	}
}

// requireAPIScope narrows a route to one exact api.scopes value for
// Bearer callers (T-086); session-authenticated principals pass through
// unchanged — the console's own RBAC already governs them, and api.*
// says nothing about a human operator using the browser.
func (s *Server) requireAPIScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		if p.APIKey == nil {
			next(w, r)
			return
		}
		if s.writeEntitlementError(w, r, p.Ent().CheckAPIScope(scope)) {
			return
		}
		if !hasScope(p.APIKey.Scopes, scope) {
			WriteError(w, http.StatusForbidden, "forbidden", "this API key does not hold the "+scope+" scope", correlationID(r))
			return
		}
		next(w, r)
	}
}

func hasScope(scopes []string, scope string) bool {
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
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
