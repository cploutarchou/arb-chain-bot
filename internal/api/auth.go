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
)

const sessionCookie = "arb_session"

type ctxKey int

const principalKey ctxKey = 1

// Principal is the authenticated caller attached to the request context.
type Principal struct {
	UserID string
	Role   auth.Role
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

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	out := map[string]string{"user_id": p.UserID, "role": string(p.Role)}
	// Re-derive the CSRF token so a reloaded console recovers it without
	// re-authenticating (it is bound to the session, not stored).
	if c, err := r.Cookie(sessionCookie); err == nil {
		out["csrf_token"] = s.csrfFor(c.Value)
	}
	WriteData(w, http.StatusOK, out)
}

// requireAuth validates the session cookie and attaches the principal.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
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
		ctx := context.WithValue(r.Context(), principalKey, Principal{UserID: sess.UserID, Role: sess.Role})
		next(w, r.WithContext(ctx))
	}
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
