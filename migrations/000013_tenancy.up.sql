-- Tenancy (T-081, docs/design/packages.md §3, docs/design/billing.md §1):
-- organisations, memberships, a platform_admin flag on users, and an
-- org_id on every screener row so store queries can be scoped per
-- tenant. Organisation 1 is the platform operator's own organisation;
-- every pre-existing user and every pre-existing screener row is
-- backfilled into it. Nothing here touches the exchange-credential
-- vault or the live-execution gate: entitlements never include live
-- execution (packages.md §7) and the exchange secrets group is
-- platform-admin only (compliance review 2026-08-27 #1).

BEGIN;

CREATE TABLE organisations (
    id                    BIGSERIAL PRIMARY KEY,
    name                  TEXT NOT NULL,
    package_code          TEXT NOT NULL DEFAULT 'watch'
                          CHECK (package_code IN ('watch', 'signal', 'operator', 'desk', 'institution')),
    country               TEXT,
    customer_type         TEXT NOT NULL DEFAULT 'business'
                          CHECK (customer_type IN ('consumer', 'business')),
    risk_ack_version      TEXT,
    risk_ack_at           TIMESTAMPTZ,
    risk_ack_ip           TEXT,
    -- 14-day Operator trial on sign-up (packages.md §2); NULL = no trial
    -- running (platform org, or already converted/expired).
    trial_ends_at         TIMESTAMPTZ,
    -- Partial document deep-merged over the package document and
    -- re-validated (packages.md §3; compliance #23).
    entitlements_override JSONB,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Organisation 1 = the platform operator. It carries the widest package
-- so the operator's own console is never gated by a tenant limit.
INSERT INTO organisations (id, name, package_code, customer_type)
VALUES (1, 'platform', 'institution', 'business');
SELECT setval('organisations_id_seq', 1);

CREATE TABLE memberships (
    org_id     BIGINT NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    user_id    TEXT   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT   NOT NULL CHECK (role IN ('OWNER', 'ADMIN', 'MEMBER', 'VIEWER')),
    status     TEXT   NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX memberships_user_idx ON memberships (user_id);

-- platform_admin is the operator's own staff flag: it is what unlocks
-- the exchange-credential vault group and the system routes. It is
-- never granted by a package, a membership role, or a webhook.
ALTER TABLE users ADD COLUMN platform_admin BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE users SET platform_admin = TRUE WHERE role = 'ADMIN';

-- Every existing user belongs to the platform organisation; the legacy
-- console role maps onto the membership role.
INSERT INTO memberships (org_id, user_id, role)
SELECT 1, id,
       CASE role WHEN 'ADMIN' THEN 'OWNER' WHEN 'OPERATOR' THEN 'ADMIN' ELSE 'VIEWER' END
FROM users;

-- Screener rows: org_id with a DEFAULT 1 backfill. The default stays so
-- that engine-side inserts that carry no org resolve to the platform
-- organisation; API-side inserts always set it explicitly.
ALTER TABLE screener_rules            ADD COLUMN org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organisations(id);
ALTER TABLE screener_templates        ADD COLUMN org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organisations(id);
ALTER TABLE screener_events           ADD COLUMN org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organisations(id);
ALTER TABLE screener_paper_positions  ADD COLUMN org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organisations(id);
ALTER TABLE screener_paper_executions ADD COLUMN org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organisations(id);
ALTER TABLE screener_paper_balances   ADD COLUMN org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organisations(id);
ALTER TABLE screener_paper_balances   DROP CONSTRAINT screener_paper_balances_pkey;
ALTER TABLE screener_paper_balances   ADD PRIMARY KEY (org_id, venue, asset);

CREATE INDEX screener_rules_org_idx            ON screener_rules (org_id, created_at);
CREATE INDEX screener_templates_org_idx        ON screener_templates (org_id, user_id, created_at DESC);
CREATE INDEX screener_events_org_idx           ON screener_events (org_id, opened_at DESC);
CREATE INDEX screener_paper_positions_org_idx  ON screener_paper_positions (org_id, opened_at DESC);
CREATE INDEX screener_paper_executions_org_idx ON screener_paper_executions (org_id, at DESC);

COMMIT;
