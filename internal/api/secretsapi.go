package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/secrets"
)

// SecretsAdmin is the write-only vault surface (T-060 §3.4). Values go
// in through Put and never come back out through any method here: List
// returns presence/source/provenance only.
type SecretsAdmin interface {
	Configured() bool
	Reason() string
	KeyID() string
	List(ctx context.Context) ([]secrets.Info, error)
	Put(ctx context.Context, name string, value []byte, actor string) (secrets.Info, error)
	Delete(ctx context.Context, name string) (secrets.Info, error)
}

// maxSecretBody bounds the PUT body (8 KiB: a 4096-char value plus JSON
// framing). Read through an io.LimitReader and zeroed after use.
const maxSecretBody = 8 << 10

// secretsRoutes: GET needs view:system; PUT/DELETE need system:config
// (ADMIN) and CSRF. Every mutation is audited as secret.write /
// secret.delete with entity "secret:{name}" — no before payload, since
// there is nothing safe to record.
func (s *Server) secretsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/secrets", s.requirePerm(auth.PermViewSystem, s.handleSecretsList))
	mux.HandleFunc("PUT /api/v1/secrets/{name}", s.requirePerm(auth.PermSystemConfig, s.requireCSRF(s.handleSecretPut)))
	mux.HandleFunc("DELETE /api/v1/secrets/{name}", s.requirePerm(auth.PermSystemConfig, s.requireCSRF(s.handleSecretDelete)))
}

// secretsVaultStatus is the shared vault-status shape (GET /secrets and
// the capabilities route).
func (s *Server) secretsVaultStatus() map[string]any {
	out := map[string]any{"vault_configured": false}
	if s.Secrets == nil {
		out["reason"] = "secrets vault not wired in this profile"
		return out
	}
	out["vault_configured"] = s.Secrets.Configured()
	if s.Secrets.Configured() {
		out["key_id"] = s.Secrets.KeyID()
	} else {
		out["reason"] = s.Secrets.Reason()
	}
	return out
}

func (s *Server) handleSecretsList(w http.ResponseWriter, r *http.Request) {
	out := s.secretsVaultStatus()
	if s.Secrets == nil {
		out["secrets"] = []secrets.Info{}
		WriteData(w, http.StatusOK, out)
		return
	}
	list, err := s.Secrets.List(r.Context())
	if err != nil {
		s.log.Error("secrets list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "secrets_failed", "listing secrets failed", correlationID(r))
		return
	}
	out["secrets"] = list
	WriteData(w, http.StatusOK, out)
}

func (s *Server) handleSecretPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.Secrets == nil {
		WriteError(w, http.StatusServiceUnavailable, "vault_unavailable", "secrets vault not wired in this profile", correlationID(r))
		return
	}
	if _, ok := secrets.Known[name]; !ok {
		WriteError(w, http.StatusNotFound, "unknown_secret", "no such secret in the registry", correlationID(r))
		return
	}
	// Bounded read into a buffer that is zeroed before the handler
	// returns; the decoded value string is not logged anywhere on any
	// path below (the log-capture test enforces this).
	buf, err := io.ReadAll(io.LimitReader(r.Body, maxSecretBody+1))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "could not read body", correlationID(r))
		return
	}
	defer zero(buf)
	if len(buf) > maxSecretBody {
		WriteError(w, http.StatusBadRequest, "bad_payload", "body too large", correlationID(r))
		return
	}
	// The value is decoded straight into a []byte (secretValue) — never
	// into a Go string, which could not be scrubbed — and zeroed with
	// the buffer before the handler returns.
	var body struct {
		Value secretValue `json:"value"`
	}
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", `body must be {"value": "..."}`, correlationID(r))
		return
	}
	defer zero(body.Value)
	principal, _ := PrincipalFrom(r.Context())
	info, err := s.Secrets.Put(r.Context(), name, body.Value, principal.UserID)
	if err != nil {
		s.writeSecretsError(w, r, err)
		return
	}
	s.auditWith(r, principal.UserID, "secret.write", "secret:"+name, secretAudit{Name: name, Present: true, KeyID: s.Secrets.KeyID()})
	s.log.Info("secret written", "name", name, "actor", principal.UserID, "key_id", s.Secrets.KeyID())
	WriteData(w, http.StatusOK, info)
}

// secretAudit is the design §3.4 audit "after" payload: name, presence
// and the key fingerprint — nothing about the value.
type secretAudit struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	KeyID   string `json:"key_id,omitempty"`
}

// secretValue decodes a JSON string into bytes without materialising a
// Go string (encoding/json would decode []byte as base64, and a string
// field would leave an unscrubbable copy). Escapes follow RFC 8259.
type secretValue []byte

func (v *secretValue) UnmarshalJSON(raw []byte) error {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return errors.New("value must be a JSON string")
	}
	out := make([]byte, 0, len(raw)-2)
	body := raw[1 : len(raw)-1]
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i >= len(body) {
			zero(out)
			return errors.New("truncated escape")
		}
		switch body[i] {
		case '"', '\\', '/':
			out = append(out, body[i])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, n, err := decodeUnicodeEscape(body[i-1:])
			if err != nil {
				zero(out)
				return err
			}
			out = utf8.AppendRune(out, r)
			i += n - 2 // n counts the leading backslash and 'u' already consumed
		default:
			zero(out)
			return errors.New("invalid escape")
		}
	}
	*v = out
	return nil
}

// decodeUnicodeEscape parses "\uXXXX" (and a following low surrogate
// when the first is a high surrogate) at the start of b; n is the
// number of bytes consumed.
func decodeUnicodeEscape(b []byte) (r rune, n int, err error) {
	hex4 := func(p []byte) (rune, bool) {
		if len(p) < 6 || p[0] != '\\' || p[1] != 'u' {
			return 0, false
		}
		var x rune
		for _, c := range p[2:6] {
			x <<= 4
			switch {
			case c >= '0' && c <= '9':
				x |= rune(c - '0')
			case c >= 'a' && c <= 'f':
				x |= rune(c-'a') + 10
			case c >= 'A' && c <= 'F':
				x |= rune(c-'A') + 10
			default:
				return 0, false
			}
		}
		return x, true
	}
	r, ok := hex4(b)
	if !ok {
		return 0, 0, errors.New("invalid \\u escape")
	}
	if utf16.IsSurrogate(r) {
		lo, ok := hex4(b[6:])
		if !ok {
			return 0, 0, errors.New("invalid surrogate pair")
		}
		if dec := utf16.DecodeRune(r, lo); dec != utf8.RuneError {
			return dec, 12, nil
		}
		return 0, 0, errors.New("invalid surrogate pair")
	}
	return r, 6, nil
}

func (s *Server) handleSecretDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.Secrets == nil {
		WriteError(w, http.StatusServiceUnavailable, "vault_unavailable", "secrets vault not wired in this profile", correlationID(r))
		return
	}
	if _, ok := secrets.Known[name]; !ok {
		WriteError(w, http.StatusNotFound, "unknown_secret", "no such secret in the registry", correlationID(r))
		return
	}
	principal, _ := PrincipalFrom(r.Context())
	info, err := s.Secrets.Delete(r.Context(), name)
	if err != nil {
		s.writeSecretsError(w, r, err)
		return
	}
	s.auditWith(r, principal.UserID, "secret.delete", "secret:"+name, secretAudit{Name: name, Present: info.Present})
	s.log.Info("secret deleted", "name", name, "actor", principal.UserID)
	WriteData(w, http.StatusOK, info)
}

func (s *Server) writeSecretsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, secrets.ErrUnknownSecret):
		WriteError(w, http.StatusNotFound, "unknown_secret", "no such secret in the registry", correlationID(r))
	case errors.Is(err, secrets.ErrInvalidValue):
		// The message names the rule, never the value.
		WriteError(w, http.StatusBadRequest, "invalid_secret", err.Error(), correlationID(r))
	case errors.Is(err, secrets.ErrVaultUnavailable):
		WriteError(w, http.StatusServiceUnavailable, "vault_unavailable", err.Error(), correlationID(r))
	default:
		// Generic echo only: a storage error must not carry anything
		// about the value, and the log line records the error, not the
		// request body.
		s.log.Error("secret write failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "secrets_failed", "secret change failed", correlationID(r))
	}
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
