package pg

import (
	"context"
	"database/sql"

	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// GrantRepo reads user_grants. The table is RLS-scoped, so every query is
// implicitly confined to the tenant bound on the request transaction (ADR-0004);
// callers must run inside that transaction (the capability middleware does).
type GrantRepo struct {
	db database.ExecerPg
}

func NewGrantRepo(db database.ExecerPg) *GrantRepo {
	return &GrantRepo{db: db}
}

// ListForUser returns every grant held by the user within the current tenant.
func (r *GrantRepo) ListForUser(ctx context.Context, userID string) ([]authz.Grant, error) {
	rows, err := r.db.QueryxContext(ctx,
		`SELECT role, scope_entity_id FROM user_grants WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var grants []authz.Grant
	for rows.Next() {
		var role string
		var scope sql.NullString
		if err := rows.Scan(&role, &scope); err != nil {
			return nil, err
		}
		g := authz.Grant{Role: authz.Role(role)}
		if scope.Valid {
			s := scope.String
			g.ScopeEntityID = &s
		}
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// Insert writes one grant. user_grants is RLS-scoped: tenant_id defaults from
// the GUC bound on the request transaction, and the isolation policy's WITH
// CHECK refuses a mismatched tenant.
func (r *GrantRepo) Insert(ctx context.Context, userID, role string, scopeEntityID *string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO user_grants (user_id, role, scope_entity_id) VALUES ($1, $2, $3)`,
		userID, role, scopeEntityID)
	return err
}
