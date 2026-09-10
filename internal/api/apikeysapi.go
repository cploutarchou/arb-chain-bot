package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/apikey"
)

// apiKeyRoutes: the client API-key surface (T-086, docs/design/packages.md
// §3.1 api.*). OWNER/ADMIN of the organisation manage keys from the
// console; the keys themselves authenticate everything else via
// Authorization: Bearer <key> (see auth.go's authenticateAPIKey).
//
//   - POST   /org/api-keys      create a key (plaintext returned ONCE)
//   - GET    /org/api-keys      list the organisation's keys (redacted)
//   - DELETE /org/api-keys/{id} revoke a key
func (s *Server) apiKeyRoutes(mux *http.ServeMux) {
	needKeys := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.APIKeys == nil || s.Tenancy == nil {
				WriteError(w, http.StatusServiceUnavailable, "api_keys_unavailable", "API keys are not available in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/org/api-keys", s.requireAuth(s.requireOrgManager(needKeys(s.handleAPIKeyList))))
	mux.HandleFunc("POST /api/v1/org/api-keys", s.requireAuth(s.requireOrgManager(s.requireCSRF(needKeys(s.handleAPIKeyCreate)))))
	mux.HandleFunc("DELETE /api/v1/org/api-keys/{id}", s.requireAuth(s.requireOrgManager(s.requireCSRF(needKeys(s.handleAPIKeyRevoke)))))
}

func (s *Server) handleAPIKeyList(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	keys, err := s.APIKeys.ListKeys(r.Context(), p.OrgID)
	if err != nil {
		s.log.Error("api key list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "api_keys_failed", "listing API keys failed", correlationID(r))
		return
	}
	out := make([]apikey.Redacted, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Redact())
	}
	WriteData(w, http.StatusOK, map[string]any{"api_keys": out, "keys_max": p.Ent().API.KeysMax})
}

func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&body); err != nil || body.Name == "" {
		WriteError(w, http.StatusBadRequest, "bad_payload", `body must be {"name": "...", "scopes": ["read", ...]}`, correlationID(r))
		return
	}
	p, _ := PrincipalFrom(r.Context())
	ent := p.Ent()
	// api.enabled gates key creation itself, before anything else
	// (packages.md §3.2 api.*): a package with client API access
	// disabled cannot mint a key regardless of the scopes requested.
	if s.writeEntitlementError(w, r, ent.CheckAPIScope("read")) {
		return
	}
	scopes := body.Scopes
	if len(scopes) == 0 {
		// Read-only by default (T-086): a key created without an
		// explicit scope list gets the least-privilege scope, never
		// everything the package allows.
		scopes = []string{"read"}
	}
	if err := apikey.ValidateScopes(scopes); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_scopes", err.Error(), correlationID(r))
		return
	}
	for _, sc := range scopes {
		if s.writeEntitlementError(w, r, ent.CheckAPIScope(sc)) {
			return
		}
	}
	existing, err := s.APIKeys.ListKeys(r.Context(), p.OrgID)
	if err != nil {
		s.log.Error("api key list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "api_keys_failed", "listing API keys failed", correlationID(r))
		return
	}
	active := 0
	for _, k := range existing {
		if k.Active() {
			active++
		}
	}
	if s.writeEntitlementError(w, r, ent.CheckAPIKeyCount(active)) {
		return
	}
	rec, plaintext, err := apikey.Generate(p.OrgID, p.UserID, body.Name, scopes)
	if err != nil {
		s.log.Error("api key generation failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "api_key_failed", "generating the API key failed", correlationID(r))
		return
	}
	rec.ID = newScreenerID("key")
	created, err := s.APIKeys.CreateKey(r.Context(), rec)
	if err != nil {
		if errors.Is(err, apikey.ErrPrefixCollision) {
			// Astronomically unlikely (>=192 bits of prefix entropy);
			// the caller retries the request and gets a fresh token.
			WriteError(w, http.StatusConflict, "api_key_collision", "key generation collided; try again", correlationID(r))
			return
		}
		s.log.Error("api key create failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "api_key_failed", "creating the API key failed", correlationID(r))
		return
	}
	// Audit records id/prefix/name/scopes only — never the hash, never
	// the plaintext, which exists nowhere after this response.
	s.auditWith(r, p.UserID, "apikey.create", "api_key:"+created.ID,
		map[string]any{"name": created.Name, "prefix": created.Prefix, "scopes": created.Scopes})
	WriteData(w, http.StatusCreated, map[string]any{
		"api_key": created.Redact(),
		"key":     plaintext,
	})
}

func (s *Server) handleAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, _ := PrincipalFrom(r.Context())
	if err := s.APIKeys.RevokeKey(r.Context(), p.OrgID, id, time.Now().UTC()); err != nil {
		if errors.Is(err, apikey.ErrNotFound) {
			WriteError(w, http.StatusNotFound, "not_found", "no such API key", correlationID(r))
			return
		}
		s.log.Error("api key revoke failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "api_key_failed", "revoking the API key failed", correlationID(r))
		return
	}
	s.auditWith(r, p.UserID, "apikey.revoke", "api_key:"+id, map[string]any{"id": id})
	WriteData(w, http.StatusOK, map[string]any{"status": "revoked"})
}
