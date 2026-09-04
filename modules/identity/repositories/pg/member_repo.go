package pg

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.MemberRepository = (*MemberRepo)(nil)

// MemberRepo is the tenant-directory persistence. users is NOT RLS-scoped, so
// every users statement carries an explicit tenant_id predicate taken from the
// requester; user_grants + entities ARE RLS-scoped and rely on the request
// transaction's tenant GUC (ADR-0004).
type MemberRepo struct {
	db database.ExecerPg
}

func NewMemberRepo(db database.ExecerPg) *MemberRepo {
	return &MemberRepo{db: db}
}

const memberColumns = `id, tenant_id, email, name, kind, status, totp_enabled, password_hash IS NOT NULL AS has_password, created_at`

// memberFilter is shared by the page and the count so both agree. NULL-tolerant
// filters let one statement serve every combination.
const memberFilter = ` FROM users
WHERE tenant_id = $1
  AND ($2::varchar IS NULL OR kind = $2::varchar)
  AND ($3::varchar IS NULL OR status = $3::varchar)`

func (r *MemberRepo) List(ctx context.Context, args domain.ListMembersArgs) ([]domain.Member, int, error) {
	var kind, status *string
	if args.Kind != "" {
		kind = &args.Kind
	}
	if args.Status != "" {
		status = &args.Status
	}

	var members []domain.Member
	if err := r.db.SelectContext(ctx, &members,
		`SELECT `+memberColumns+memberFilter+` ORDER BY name, email, id LIMIT $4 OFFSET $5`,
		args.TenantID, kind, status, args.Limit, args.Offset); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.db.GetContext(ctx, &total,
		`SELECT COUNT(*)::int`+memberFilter, args.TenantID, kind, status); err != nil {
		return nil, 0, err
	}

	if err := r.attachGrants(ctx, members); err != nil {
		return nil, 0, err
	}
	return members, total, nil
}

func (r *MemberRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.Member, error) {
	var m domain.Member
	err := r.db.GetContext(ctx, &m,
		`SELECT `+memberColumns+` FROM users WHERE id = $1 AND tenant_id = $2 LIMIT 1`, id, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil means not found (or another tenant's user)
	}
	if err != nil {
		return nil, err
	}
	members := []domain.Member{m}
	if err := r.attachGrants(ctx, members); err != nil {
		return nil, err
	}
	return &members[0], nil
}

// attachGrants loads every grant of the given members in ONE statement (no
// N+1). user_grants is RLS-scoped (only this tenant's rows are visible) and the
// scope entity's name comes from an RLS-scoped LEFT JOIN, so a scope pointing
// outside the tenant could never render a foreign name.
func (r *MemberRepo) attachGrants(ctx context.Context, members []domain.Member) error {
	if len(members) == 0 {
		return nil
	}
	ids := make([]string, 0, len(members))
	index := make(map[string]int, len(members))
	for i := range members {
		members[i].Grants = []domain.MemberGrant{}
		ids = append(ids, members[i].ID)
		index[members[i].ID] = i
	}

	var grants []domain.MemberGrant
	if err := r.db.SelectContext(ctx, &grants, `
		SELECT g.id, g.user_id, g.role, g.scope_entity_id, e.name AS scope_entity_name
		FROM user_grants g
		LEFT JOIN entities e ON e.id = g.scope_entity_id
		WHERE g.user_id = ANY($1::uuid[])
		ORDER BY g.created_at, g.id`, ids); err != nil {
		return err
	}
	for _, g := range grants {
		if i, ok := index[g.UserID]; ok {
			members[i].Grants = append(members[i].Grants, g)
		}
	}
	return nil
}

func (r *MemberRepo) UpdateNameStatus(ctx context.Context, tenantID, id, name, status string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET name = $3, status = $4, updated_at = NOW() WHERE id = $1 AND tenant_id = $2`,
		id, tenantID, name, status)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (r *MemberRepo) Activate(ctx context.Context, id, passwordHash string, name *string) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET password_hash = $2, status = 'active', name = COALESCE($3, name),
		    failed_login_attempts = 0, locked_until = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'invited' AND kind = 'human'`,
		id, passwordHash, name)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// InsertGrant writes one grant on the request transaction: tenant_id defaults
// from the GUC and the composite (tenant_id, scope_entity_id) FK rejects a
// scope entity of another tenant — surfaced as a 400.
func (r *MemberRepo) InsertGrant(ctx context.Context, userID, role string, scopeEntityID *string) (string, error) {
	var id string
	err := r.db.GetContext(ctx, &id,
		`INSERT INTO user_grants (user_id, role, scope_entity_id) VALUES ($1, $2, $3) RETURNING id`,
		userID, role, scopeEntityID)
	if err != nil {
		if database.IsForeignKeyViolation(err) {
			return "", apperrors.NewValidation(domain.MsgScopeEntityNotInTenant)
		}
		return "", err
	}
	return id, nil
}

func (r *MemberRepo) DeleteGrant(ctx context.Context, userID, grantID string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM user_grants WHERE id = $1 AND user_id = $2`, grantID, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (r *MemberRepo) DeleteGrantsForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM user_grants WHERE user_id = $1`, userID)
	return err
}

// CountActiveTenantAdmins: the last-admin guard's source of truth — active,
// human, holding a tenant-wide tenant_admin grant. Read on the request
// transaction so it sees this request's own (uncommitted) grant changes.
func (r *MemberRepo) CountActiveTenantAdmins(ctx context.Context, tenantID string) (int, error) {
	var n int
	err := r.db.GetContext(ctx, &n, `
		SELECT COUNT(DISTINCT u.id)::int
		FROM users u
		JOIN user_grants g ON g.user_id = u.id AND g.tenant_id = u.tenant_id
		WHERE u.tenant_id = $1 AND u.kind = 'human' AND u.status = 'active'
		  AND g.role = 'tenant_admin' AND g.scope_entity_id IS NULL`, tenantID)
	return n, err
}

// LockAdminGuard takes a per-tenant transaction-scoped advisory lock (released
// at commit/rollback), so two requests that each remove one of the last two
// admins cannot both see "one admin remains".
func (r *MemberRepo) LockAdminGuard(ctx context.Context, tenantID string) error {
	_, err := r.db.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('member-admin-guard:' || $1, 0))`, tenantID)
	return err
}

// AssigneeChecker answers the task-assignment question for the task-instances
// module (port taskinstancesdomain.AssigneeChecker): may this user id be
// assigned a task in the requester's tenant? users is NOT RLS-scoped, so the
// tenant predicate comes explicitly from the request context.
type AssigneeChecker struct {
	db database.ExecerPg
}

func NewAssigneeChecker(db database.ExecerPg) *AssigneeChecker {
	return &AssigneeChecker{db: db}
}

func (c *AssigneeChecker) IsAssignable(ctx context.Context, userID string) (bool, error) {
	tenantID := app.GetTenantID(ctx)
	if tenantID == "" || userID == "" {
		return false, nil
	}
	var ok bool
	err := c.db.GetContext(ctx, &ok, `
		SELECT EXISTS (
			SELECT 1 FROM users WHERE id = $1 AND tenant_id = $2 AND kind = 'human' AND status = 'active'
		)`, userID, tenantID)
	return ok, err
}
