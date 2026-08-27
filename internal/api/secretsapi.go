package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

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
	Put(ctx context.Context, name, value, actor string) (secrets.Info, error)
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
	var body struct {
		Value string `json:"value"`
	}
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", `body must be {"value": "..."}`, correlationID(r))
		return
	}
	principal, _ := PrincipalFrom(r.Context())
	info, err := s.Secrets.Put(r.Context(), name, body.Value, principal.UserID)
	body.Value = ""
	if err != nil {
		s.writeSecretsError(w, r, err)
		return
	}
	s.audit(r, principal.UserID, "secret.write", "secret:"+name)
	s.log.Info("secret written", "name", name, "actor", principal.UserID, "key_id", s.Secrets.KeyID())
	WriteData(w, http.StatusOK, info)
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
	s.audit(r, principal.UserID, "secret.delete", "secret:"+name)
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
