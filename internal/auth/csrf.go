package auth

import "crypto/subtle"

// CSRF double-submit: the session cookie is paired with a per-session
// CSRF token; mutating browser requests echo it in X-CSRF-Token. The
// token is derived at login, stored alongside the session client-side
// (readable cookie or bootstrap payload), and compared in constant time
// (docs/security.md §4).

// VerifyCSRF compares the submitted header token with the session-bound
// token in constant time. Empty values fail closed.
func VerifyCSRF(sessionToken, headerToken string) error {
	if sessionToken == "" || headerToken == "" {
		return ErrInvalidCSRF
	}
	if subtle.ConstantTimeCompare([]byte(sessionToken), []byte(headerToken)) != 1 {
		return ErrInvalidCSRF
	}
	return nil
}
