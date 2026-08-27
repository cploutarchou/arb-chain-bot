-- Paddle billing mirror (T-083, docs/design/packages.md §4,
-- docs/design/billing.md). Paddle is the source of truth for money;
-- these rows are a mirror written ONLY by verified, idempotent webhooks
-- (paddle_events is the dedup table) and read by the entitlement
-- resolver. No card data exists anywhere in this schema: Paddle hosts
-- checkout and stores payment methods.

BEGIN;

-- price_id -> package mapping. The operator fills this after creating
-- the Paddle catalogue (billing.md §3); a webhook naming an unmapped
-- price is stored and flagged rather than guessed.
CREATE TABLE billing_prices (
    price_id         TEXT PRIMARY KEY,
    package_code     TEXT NOT NULL CHECK (package_code IN ('signal', 'operator', 'desk', 'institution')),
    billing_interval TEXT NOT NULL CHECK (billing_interval IN ('month', 'year')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One subscription per organisation (packages.md sells one package per
-- organisation; seat add-ons are line items on the same subscription).
CREATE TABLE subscriptions (
    org_id                 BIGINT PRIMARY KEY REFERENCES organisations(id) ON DELETE CASCADE,
    paddle_customer_id     TEXT,
    paddle_subscription_id TEXT UNIQUE,
    price_id               TEXT,
    -- Paddle status vocabulary verbatim.
    status                 TEXT NOT NULL CHECK (status IN ('trialing', 'active', 'past_due', 'paused', 'canceled')),
    current_period_end     TIMESTAMPTZ,
    cancel_at_period_end   BOOLEAN NOT NULL DEFAULT FALSE,
    -- Set when status first became past_due; cleared on recovery. The
    -- resolver turns the organisation read-only 7 days after it
    -- (packages.md §4).
    past_due_since         TIMESTAMPTZ,
    -- A downgrade scheduled for the next billing period (§4): the
    -- price the subscription will switch to.
    scheduled_price_id     TEXT,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Webhook idempotency: event_id is Paddle's evt_... id, identical on
-- every retry. processed_at NULL = received but not yet applied (a
-- crash between insert and apply reprocesses on the retry).
CREATE TABLE paddle_events (
    event_id     TEXT PRIMARY KEY,
    type         TEXT NOT NULL,
    occurred_at  TIMESTAMPTZ,
    payload      JSONB NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ
);
CREATE INDEX paddle_events_type_idx ON paddle_events (type, received_at DESC);

-- Affiliate ledger (T-084, packages.md §5): insert-only, decimal USD,
-- every entry cites the Paddle transaction it derives from.
CREATE TABLE affiliate_accounts (
    id            TEXT PRIMARY KEY,
    org_id        BIGINT NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    code          TEXT NOT NULL UNIQUE,
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'review', 'closed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE organisations ADD COLUMN referred_by TEXT REFERENCES affiliate_accounts(id);
CREATE TABLE affiliate_ledger (
    id             TEXT PRIMARY KEY,
    affiliate_id   TEXT NOT NULL REFERENCES affiliate_accounts(id),
    referred_org   BIGINT NOT NULL REFERENCES organisations(id),
    transaction_id TEXT NOT NULL,
    entry          TEXT NOT NULL CHECK (entry IN ('accrued', 'matured', 'reversed', 'paid')),
    amount_usd     NUMERIC NOT NULL,
    net_usd        NUMERIC NOT NULL,
    rate           NUMERIC NOT NULL,
    matures_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX affiliate_ledger_affiliate_idx ON affiliate_ledger (affiliate_id, created_at);
CREATE INDEX affiliate_ledger_txn_idx ON affiliate_ledger (transaction_id);

COMMIT;
