package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/cploutarchou/arb-chain-bot/internal/billing/paddle"
	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
)

// billingRoutes (T-083, docs/design/billing.md):
//
//   - POST /billing/webhook       Paddle notification destination: no
//     session, no CSRF — the Paddle-Signature HMAC is the auth.
//   - GET  /billing/subscription  the organisation's mirror row + prices
//   - POST /billing/checkout      {price_id} → transaction for the
//     Paddle.js overlay, or a plan change on the live subscription
//   - GET  /billing/portal        one-time customer portal URL
//   - POST /billing/cancel        cancel at period end
//   - GET/PUT /billing/prices     platform operator: price → package map
//
// Checkout/portal/cancel need an organisation OWNER/ADMIN. No card data
// touches this process: Paddle hosts the checkout and payment methods.
func (s *Server) billingRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Billing == nil {
				WriteError(w, http.StatusServiceUnavailable, "billing_unavailable", "billing is not wired in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /api/v1/billing/webhook", gate(func(w http.ResponseWriter, r *http.Request) {
		s.Billing.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /api/v1/billing/subscription", s.requireAuth(gate(s.handleBillingSubscription)))
	mux.HandleFunc("POST /api/v1/billing/checkout", s.requireAuth(s.requireOrgManager(s.requireCSRF(gate(s.handleBillingCheckout)))))
	mux.HandleFunc("GET /api/v1/billing/portal", s.requireAuth(s.requireOrgManager(gate(s.handleBillingPortal))))
	mux.HandleFunc("POST /api/v1/billing/cancel", s.requireAuth(s.requireOrgManager(s.requireCSRF(gate(s.handleBillingCancel)))))
	mux.HandleFunc("GET /api/v1/billing/prices", s.requireAuth(gate(s.handleBillingPrices)))
	mux.HandleFunc("PUT /api/v1/billing/prices/{price_id}", s.requirePlatformAdmin(s.requireCSRF(gate(s.handleBillingPriceSet))))
}

func (s *Server) handleBillingSubscription(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	sub, found, err := s.Billing.Store.Subscription(r.Context(), p.OrgID)
	if err != nil {
		s.writeBillingError(w, r, err)
		return
	}
	out := map[string]any{"org_id": p.OrgID, "package_code": p.Org.PackageCode, "trial_ends_at": p.Org.TrialEndsAt,
		"configured": s.Billing.Configured(r.Context()), "status": p.Ent().Status}
	if found {
		out["subscription"] = sub
	}
	WriteData(w, http.StatusOK, out)
}

func (s *Server) handleBillingCheckout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PriceID string `json:"price_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.PriceID == "" {
		WriteError(w, http.StatusBadRequest, "bad_payload", `body must be {"price_id": "pri_..."}`, correlationID(r))
		return
	}
	p, _ := PrincipalFrom(r.Context())
	if p.OrgID == 1 {
		WriteError(w, http.StatusConflict, "platform_org", "the platform organisation has no subscription", correlationID(r))
		return
	}
	// The trial clock is the organisation's stored package only while
	// the trial runs; the effective package drives the proration mode.
	current := p.Ent().Status.Effective
	if current == "" {
		current = entitlements.PackageWatch
	}
	res, err := s.Billing.Checkout(r.Context(), p.OrgID, current, body.PriceID, "")
	if err != nil {
		s.writeBillingError(w, r, err)
		return
	}
	s.auditWith(r, p.UserID, "billing.checkout", "org:"+itoa(p.OrgID), map[string]any{"price_id": body.PriceID, "pending": res.Pending, "proration": res.Proration})
	WriteData(w, http.StatusOK, res)
}

func (s *Server) handleBillingPortal(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	url, err := s.Billing.PortalURL(r.Context(), p.OrgID)
	if err != nil {
		s.writeBillingError(w, r, err)
		return
	}
	// Only the URL (paddle:customer-portal): session ids and the
	// customer id stay server-side.
	WriteData(w, http.StatusOK, map[string]any{"url": url})
}

func (s *Server) handleBillingCancel(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if err := s.Billing.Cancel(r.Context(), p.OrgID); err != nil {
		s.writeBillingError(w, r, err)
		return
	}
	s.audit(r, p.UserID, "billing.cancel", "org:"+itoa(p.OrgID))
	WriteData(w, http.StatusOK, map[string]any{"status": "cancel_scheduled", "effective": "next_billing_period"})
}

func (s *Server) handleBillingPrices(w http.ResponseWriter, r *http.Request) {
	prices, err := s.Billing.Store.ListPrices(r.Context())
	if err != nil {
		s.writeBillingError(w, r, err)
		return
	}
	WriteData(w, http.StatusOK, map[string]any{"prices": prices, "environment": s.Billing.Environment, "client_token": s.Billing.ClientToken})
}

func (s *Server) handleBillingPriceSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PackageCode string `json:"package_code"`
		Interval    string `json:"billing_interval"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", `body must be {"package_code": "...", "billing_interval": "month|year"}`, correlationID(r))
		return
	}
	if entitlements.Rank(body.PackageCode) < 1 || (body.Interval != "month" && body.Interval != "year") {
		WriteError(w, http.StatusBadRequest, "invalid_price", "package_code must be a paid package and billing_interval month or year", correlationID(r))
		return
	}
	id := r.PathValue("price_id")
	if err := s.Billing.Store.SetPrice(r.Context(), id, body.PackageCode, body.Interval); err != nil {
		s.writeBillingError(w, r, err)
		return
	}
	p, _ := PrincipalFrom(r.Context())
	s.auditWith(r, p.UserID, "billing.price.map", "price:"+id, body)
	WriteData(w, http.StatusOK, paddle.Price{PriceID: id, PackageCode: body.PackageCode, Interval: body.Interval})
}

func (s *Server) writeBillingError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *paddle.APIError
	switch {
	case errors.Is(err, paddle.ErrUnmappedPrice):
		WriteError(w, http.StatusBadRequest, "unmapped_price", "that price is not mapped to a package", correlationID(r))
	case errors.Is(err, paddle.ErrNoCustomer), errors.Is(err, paddle.ErrNoSubscription):
		WriteError(w, http.StatusConflict, "no_subscription", "subscribe first", correlationID(r))
	case errors.Is(err, paddle.ErrBadRequest):
		WriteError(w, http.StatusBadRequest, "billing_request", err.Error(), correlationID(r))
	case paddle.IsNotConfigured(err):
		WriteError(w, http.StatusServiceUnavailable, "billing_unconfigured", "Paddle secrets are not configured", correlationID(r))
	case errors.As(err, &apiErr):
		s.log.Error("paddle api error", "status", apiErr.Status, "code", apiErr.Code)
		WriteError(w, http.StatusBadGateway, "paddle_error", "Paddle refused the request: "+apiErr.Code, correlationID(r))
	default:
		s.log.Error("billing failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "billing_failed", "billing request failed", correlationID(r))
	}
}

// readLimited reads at most n bytes of the body.
func readLimited(r *http.Request, n int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r.Body, n+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > n {
		return nil, errors.New("body too large")
	}
	return b, nil
}
