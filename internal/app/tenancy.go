package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/billing/affiliate"
	"github.com/cploutarchou/arb-chain-bot/internal/billing/paddle"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
	"github.com/cploutarchou/arb-chain-bot/internal/secrets"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// RiskDisclosureVersion is the risk-disclosure text version every
// organisation must acknowledge (compliance review 2026-08-27 #3). Bump
// it when docs/legal changes materially; every organisation is then
// re-prompted on its next request.
const RiskDisclosureVersion = "2026-08-27"

// tenantWiring bundles the tenancy store, the entitlement resolver and
// the Paddle service for the components that need them.
type tenantWiring struct {
	store    tenancy.Store
	resolver *entitlements.Resolver
	billing  *paddle.Service
}

// buildTenancy: pgx-backed stores when persistence is on (migrations
// 000013/000014), nil otherwise — api.Server then treats every account
// as the platform organisation and billing routes answer 503.
func buildTenancy(log *slog.Logger, store *storage.Store, sec *secrets.Manager, cfg config.Bootstrap) tenantWiring {
	if store == nil {
		log.Warn("tenancy disabled: no database; every account acts in the platform organisation")
		return tenantWiring{}
	}
	ts := store.Tenancy()
	resolver := entitlements.NewResolver(ts)
	resolver.OnError = func(orgID int64, err error) {
		log.Error("entitlements override rejected; using bare package", "org_id", orgID, "error", err)
	}
	env := cfg.PaddleEnv
	base := paddle.SandboxAPI
	if env == "production" {
		base = paddle.LiveAPI
	} else {
		env = "sandbox"
	}
	get := func(name string) func(ctx context.Context) string {
		return func(ctx context.Context) string {
			v, _, _ := sec.Get(ctx, name)
			return v
		}
	}
	billing := &paddle.Service{
		Store:         store.Billing(),
		Packages:      ts,
		Client:        paddle.NewClient(base, get("paddle_api_key")),
		Log:           log,
		Now:           time.Now,
		WebhookSecret: get("paddle_webhook_secret"),
		Invalidate:    resolver.Invalidate,
		ClientToken:   cfg.PaddleClientToken,
		Environment:   env,
	}
	// T-084: commissions accrue from verified transaction.completed
	// events only; an unreferred organisation accrues nothing.
	aff := &affiliate.Service{Terms: affiliate.DefaultTerms, Ledger: store.Affiliate(), Referral: store.Affiliate().Referral, IDGen: newULID}
	billing.OnTransaction = func(ctx context.Context, orgID int64, txn, net string, at time.Time) {
		if err := aff.OnTransaction(ctx, orgID, txn, net, at); err != nil {
			log.Error("affiliate accrual failed", "org_id", orgID, "transaction_id", txn, "error", err)
		}
	}
	return tenantWiring{store: ts, resolver: resolver, billing: billing}
}

// paperEntitle is the executor's precondition (packages.md §3.2
// auto_paper.*): resolve the rule's organisation, then check strategy,
// concurrent open positions and the size cap. Without tenancy every
// rule belongs to the platform organisation and nothing is gated.
func (t tenantWiring) paperEntitle(ledger paperexec.Ledger) paperexec.EntitlementCheck {
	if t.store == nil || t.resolver == nil {
		return nil
	}
	return func(ctx context.Context, ruleID, strategy string, size decimal.Decimal) (string, bool) {
		orgID, err := t.store.OrgOfRule(ctx, ruleID)
		if err != nil {
			return "organisation lookup failed: " + err.Error(), false
		}
		if orgID == tenancy.PlatformOrgID {
			return "", true
		}
		ent, err := t.resolver.For(ctx, orgID)
		if err != nil {
			return "entitlements unavailable: " + err.Error(), false
		}
		if err := ent.CheckAutoPaper(strategy, size); err != nil {
			return err.Error(), false
		}
		open, err := ledger.ListPositions(tenancy.WithOrg(ctx, orgID), "", paperexec.StatusOpen, 0)
		if err != nil {
			return "open positions lookup failed: " + err.Error(), false
		}
		if err := ent.CheckOpenPositions(len(open)); err != nil {
			return fmt.Sprintf("%v (open=%d)", err, len(open)), false
		}
		return "", true
	}
}
