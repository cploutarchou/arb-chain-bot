package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// Tenancy implements tenancy.Store and entitlements.Source over
// organisations / memberships / subscriptions (migrations 000013-14).
type Tenancy struct{ s *Store }

func (s *Store) Tenancy() *Tenancy { return &Tenancy{s: s} }

var (
	_ tenancy.Store       = (*Tenancy)(nil)
	_ entitlements.Source = (*Tenancy)(nil)
)

const orgColumns = `id, name, package_code, COALESCE(country,''), customer_type,
	COALESCE(risk_ack_version,''), risk_ack_at, COALESCE(risk_ack_ip,''), trial_ends_at,
	entitlements_override, created_at`

func scanOrg(row pgx.Row) (tenancy.Org, error) {
	var o tenancy.Org
	var ct string
	if err := row.Scan(&o.ID, &o.Name, &o.PackageCode, &o.Country, &ct, &o.RiskAckVersion, &o.RiskAckAt,
		&o.RiskAckIP, &o.TrialEndsAt, &o.EntitlementsOverride, &o.CreatedAt); err != nil {
		return tenancy.Org{}, err
	}
	o.CustomerType = tenancy.CustomerType(ct)
	return o, nil
}

func (t *Tenancy) ContextForUser(ctx context.Context, userID string) (tenancy.Context, error) {
	var out tenancy.Context
	var role string
	err := t.s.Pool.QueryRow(ctx, `
		SELECT m.org_id, m.user_id, m.role, m.status = 'suspended', m.created_at,
		       o.id, o.name, o.package_code, COALESCE(o.country,''), o.customer_type,
		       COALESCE(o.risk_ack_version,''), o.risk_ack_at, COALESCE(o.risk_ack_ip,''), o.trial_ends_at,
		       o.entitlements_override, o.created_at
		FROM memberships m JOIN organisations o ON o.id = m.org_id
		WHERE m.user_id = $1
		ORDER BY m.created_at ASC, m.org_id ASC LIMIT 1`, userID).
		Scan(&out.Membership.OrgID, &out.Membership.UserID, &role, &out.Membership.Suspended, &out.Membership.CreatedAt,
			&out.Org.ID, &out.Org.Name, &out.Org.PackageCode, &out.Org.Country, (*string)(&out.Org.CustomerType),
			&out.Org.RiskAckVersion, &out.Org.RiskAckAt, &out.Org.RiskAckIP, &out.Org.TrialEndsAt,
			&out.Org.EntitlementsOverride, &out.Org.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenancy.Context{}, tenancy.ErrNoMembership
	}
	if err != nil {
		return tenancy.Context{}, err
	}
	out.Membership.Role = tenancy.Role(role)
	return out, nil
}

func (t *Tenancy) Org(ctx context.Context, id int64) (tenancy.Org, error) {
	o, err := scanOrg(t.s.Pool.QueryRow(ctx, `SELECT `+orgColumns+` FROM organisations WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return tenancy.Org{}, tenancy.ErrUnknownOrg
	}
	return o, err
}

func (t *Tenancy) CreateOrg(ctx context.Context, o tenancy.Org, ownerUserID string) (tenancy.Org, error) {
	tx, err := t.s.Pool.Begin(ctx)
	if err != nil {
		return tenancy.Org{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if o.CustomerType == "" {
		o.CustomerType = tenancy.CustomerBusiness
	}
	if o.PackageCode == "" {
		o.PackageCode = entitlements.PackageWatch
	}
	created, err := scanOrg(tx.QueryRow(ctx, `
		INSERT INTO organisations (name, package_code, country, customer_type, trial_ends_at, entitlements_override)
		VALUES ($1, $2, NULLIF($3,''), $4, $5, $6)
		RETURNING `+orgColumns, o.Name, o.PackageCode, o.Country, string(o.CustomerType), o.TrialEndsAt, nilIfEmpty(o.EntitlementsOverride)))
	if err != nil {
		return tenancy.Org{}, err
	}
	if ownerUserID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'OWNER')`, created.ID, ownerUserID); err != nil {
			return tenancy.Org{}, err
		}
	}
	return created, tx.Commit(ctx)
}

func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (t *Tenancy) SetRiskAck(ctx context.Context, orgID int64, version string, at time.Time, ip string) error {
	tag, err := t.s.Pool.Exec(ctx, `
		UPDATE organisations SET risk_ack_version = $2, risk_ack_at = $3, risk_ack_ip = $4 WHERE id = $1`,
		orgID, version, at, ip)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tenancy.ErrUnknownOrg
	}
	return nil
}

func (t *Tenancy) SetPackage(ctx context.Context, orgID int64, packageCode string, trialEndsAt *time.Time) error {
	tag, err := t.s.Pool.Exec(ctx, `UPDATE organisations SET package_code = $2, trial_ends_at = $3 WHERE id = $1`, orgID, packageCode, trialEndsAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tenancy.ErrUnknownOrg
	}
	return nil
}

func (t *Tenancy) SetOverride(ctx context.Context, orgID int64, override []byte) error {
	tag, err := t.s.Pool.Exec(ctx, `UPDATE organisations SET entitlements_override = $2 WHERE id = $1`, orgID, nilIfEmpty(override))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tenancy.ErrUnknownOrg
	}
	return nil
}

func (t *Tenancy) ListMembers(ctx context.Context, orgID int64) ([]tenancy.Membership, error) {
	rows, err := t.s.Pool.Query(ctx, `
		SELECT m.org_id, m.user_id, u.email, m.role, m.status = 'suspended', m.created_at
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.org_id = $1 ORDER BY m.created_at ASC, m.user_id ASC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []tenancy.Membership{}
	for rows.Next() {
		var m tenancy.Membership
		var role string
		if err := rows.Scan(&m.OrgID, &m.UserID, &m.Email, &role, &m.Suspended, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Role = tenancy.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (t *Tenancy) AddMember(ctx context.Context, m tenancy.Membership) error {
	if !m.Role.Valid() {
		return tenancy.ErrInvalidRole
	}
	_, err := t.s.Pool.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3)`, m.OrgID, m.UserID, string(m.Role))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return tenancy.ErrDuplicate
		case "23503":
			return tenancy.ErrUnknownOrg
		}
	}
	return err
}

func (t *Tenancy) SetMemberRole(ctx context.Context, orgID int64, userID string, role tenancy.Role) error {
	if !role.Valid() {
		return tenancy.ErrInvalidRole
	}
	tx, err := t.s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cur, others, err := ownerGuard(ctx, tx, orgID, userID)
	if err != nil {
		return err
	}
	if cur == string(tenancy.RoleOwner) && role != tenancy.RoleOwner && others == 0 {
		return tenancy.ErrLastOwner
	}
	if _, err := tx.Exec(ctx, `UPDATE memberships SET role = $3 WHERE org_id = $1 AND user_id = $2`, orgID, userID, string(role)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (t *Tenancy) RemoveMember(ctx context.Context, orgID int64, userID string) error {
	tx, err := t.s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cur, others, err := ownerGuard(ctx, tx, orgID, userID)
	if err != nil {
		return err
	}
	if cur == string(tenancy.RoleOwner) && others == 0 {
		return tenancy.ErrLastOwner
	}
	if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE org_id = $1 AND user_id = $2`, orgID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ownerGuard locks the target membership and every owner row of the
// organisation in one ordered statement (same deadlock reasoning as
// lastAdminGuardRows) and reports the target's role and the number of
// OTHER owners.
func ownerGuard(ctx context.Context, tx pgx.Tx, orgID int64, userID string) (role string, otherOwners int, err error) {
	rows, err := tx.Query(ctx, `
		SELECT user_id, role FROM memberships
		WHERE org_id = $1 AND (user_id = $2 OR role = 'OWNER')
		ORDER BY user_id FOR UPDATE`, orgID, userID)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var id, r string
		if err := rows.Scan(&id, &r); err != nil {
			return "", 0, err
		}
		if id == userID {
			role, found = r, true
		} else if r == "OWNER" {
			otherOwners++
		}
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	if !found {
		return "", 0, tenancy.ErrNoMembership
	}
	return role, otherOwners, nil
}

func (t *Tenancy) OrgOfRule(ctx context.Context, ruleID string) (int64, error) {
	var id int64
	err := t.s.Pool.QueryRow(ctx, `SELECT org_id FROM screener_rules WHERE id = $1`, ruleID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenancy.PlatformOrgID, nil
	}
	return id, err
}

// EntitlementInput joins the organisation with its subscription mirror
// for the resolver (entitlements.Source).
func (t *Tenancy) EntitlementInput(ctx context.Context, orgID int64) (entitlements.Input, error) {
	var in entitlements.Input
	var status *string
	err := t.s.Pool.QueryRow(ctx, `
		SELECT o.id, o.package_code, o.entitlements_override, o.trial_ends_at, s.status, s.past_due_since
		FROM organisations o LEFT JOIN subscriptions s ON s.org_id = o.id
		WHERE o.id = $1`, orgID).Scan(&in.OrgID, &in.PackageCode, &in.Override, &in.TrialEndsAt, &status, &in.PastDueSince)
	if errors.Is(err, pgx.ErrNoRows) {
		return entitlements.Input{}, tenancy.ErrUnknownOrg
	}
	if err != nil {
		return entitlements.Input{}, err
	}
	if status != nil {
		in.SubStatus = *status
	}
	return in, nil
}
