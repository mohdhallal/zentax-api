package pg

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	authzpg "github.com/mohamadhallal/zentax-api/platform/authz/pg"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
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

// memberWhere renders the directory predicate the page and the count share, so
// both always agree. The tenant is always bound ($1); kind, status and the
// search term are appended only when set (never `$n IS NULL OR col = $n` — a
// set filter is a real predicate, an unset one is absent). The search is one
// bind matched literally (ILIKE, escaped) against name and email.
func memberWhere(args domain.ListMembersArgs) (string, []any) {
	where := ` FROM users WHERE tenant_id = $1`
	params := []any{args.TenantID}
	if args.Kind != "" {
		params = append(params, args.Kind)
		where += ` AND kind = $` + strconv.Itoa(len(params))
	}
	if args.Status != "" {
		params = append(params, args.Status)
		where += ` AND status = $` + strconv.Itoa(len(params))
	}
	if term := strings.TrimSpace(args.Search); term != "" {
		params = append(params, "%"+baserepo.EscapeLike(term)+"%")
		n := `$` + strconv.Itoa(len(params))
		where += ` AND (name ILIKE ` + n + ` ESCAPE '\' OR email ILIKE ` + n + ` ESCAPE '\')`
	}
	return where, params
}

func (r *MemberRepo) List(ctx context.Context, args domain.ListMembersArgs) ([]domain.Member, int, error) {
	where, params := memberWhere(args)

	var members []domain.Member
	pageParams := append(append([]any{}, params...), args.Limit, args.Offset)
	if err := r.db.SelectContext(ctx, &members,
		`SELECT `+memberColumns+where+
			` ORDER BY name, email, id LIMIT $`+strconv.Itoa(len(params)+1)+` OFFSET $`+strconv.Itoa(len(params)+2),
		pageParams...); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.db.GetContext(ctx, &total, `SELECT COUNT(*)::int`+where, params...); err != nil {
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
// N+1), and WITHHOLDS the scope entity's name when that entity lies outside the
// caller's read scope.
//
// The directory itself is deliberately never narrowed (platform/authz/
// readscope.go): a preparer scoped to one subsidiary still has to render the
// assignee picker, so every member of the tenant is listed for every principal.
// But a grant row is not directory data — it carries an ENTITY, and resolving
// e.name for it handed that scoped principal the human-readable names of the
// siblings they cannot read: in ADR-0012's motivating case (an external advisor
// granted one entity) precisely the list of client entities the advisor is not
// engaged on. So the name is resolved only inside the caller's entity:read
// scope — the same predicate and the same capability the /entities reads use —
// and for anything else it never leaves the database. The caller's own grants
// and every tenant-wide grant (scope_entity_id IS NULL) are untouched, as is an
// unbounded caller, who pays no query for the scope at all.
//
// user_grants and entities are both RLS-scoped, so a scope pointing outside the
// TENANT could never render a foreign name to begin with; this is the in-tenant
// half of the same question.
//
// CONTRACT WITH dto.MemberToJSON: a grant that has a scope entity but no
// resolved name is a withheld scope, and the DTO drops the id on that signal
// too. The combination has no other meaning — entities.name is NOT NULL, and
// the composite (tenant_id, scope_entity_id) FK cascades, so an in-tenant grant
// whose scope the caller may read always resolves a name. ScopeEntityID is left
// intact on the domain object so the write paths keep full fidelity (the
// last-admin guard asks removed.IsTenantWide()); those are member:manage paths,
// which only a tenant-wide — hence unbounded — admin can reach.
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

	scope, err := authzpg.ReadScope(ctx, r.db, authz.EntityRead)
	if err != nil {
		return err
	}
	args := []any{ids}
	bind := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	// "" (unbounded) reads every name; "FALSE" resolves none.
	nameVisible := "TRUE"
	if pred := scope.EntityPredicate("g.scope_entity_id", bind); pred != "" {
		nameVisible = pred
	}

	var grants []domain.MemberGrant
	if err := r.db.SelectContext(ctx, &grants, `
		SELECT g.id, g.user_id, g.role, g.scope_entity_id,
		       CASE WHEN `+nameVisible+` THEN e.name END AS scope_entity_name
		FROM user_grants g
		LEFT JOIN entities e ON e.id = g.scope_entity_id
		WHERE g.user_id = ANY($1::uuid[])
		ORDER BY g.created_at, g.id`, args...); err != nil {
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
