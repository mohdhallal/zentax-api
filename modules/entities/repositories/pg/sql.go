package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/platform/authz"
	authzpg "github.com/mohamadhallal/zentax-api/platform/authz/pg"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

// entityColumns is the domain projection — deliberately WITHOUT tenant_id, which
// is infrastructure (RLS-enforced) and absent from the domain.Entity struct, so
// selecting it would break sqlx struct scanning. custom_periods (JSONB) scans
// into the domain.CustomPeriods Scanner/Valuer type.
const entityColumns = `id, parent_entity_id, name, legal_name, country, tax_residency, ` +
	`fiscal_calendar_pattern, financial_year_end, fiscal_week_end_day, fiscal_year_end_rule, custom_periods, ` +
	`status, created_at, updated_at, created_by, updated_by`

var sqlConfig = baserepo.SQLConfig{
	AllowedColumns: map[string]bool{
		"created_at":       true,
		"name":             true,
		"country":          true,
		"status":           true,
		"parent_entity_id": true,
	},
	DefaultOrderBy:   "created_at",
	DefaultOrderDesc: true, // newest first
	// `search` matches the display name or the legal name (ILIKE, escaped).
	SearchColumns: []string{"name", "legal_name"},
	GetById:       `SELECT ` + entityColumns + ` FROM entities WHERE id = $1 LIMIT 1`,
	// tenant_id is omitted on purpose: it defaults from the app.tenant_id GUC.
	Create: `
		INSERT INTO entities (parent_entity_id, name, legal_name, country, tax_residency, fiscal_calendar_pattern, financial_year_end,
		                      fiscal_week_end_day, fiscal_year_end_rule, custom_periods)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING ` + entityColumns,
	Update: `
		UPDATE entities
		SET parent_entity_id = $2,
		    name = $3,
		    legal_name = $4,
		    country = $5,
		    tax_residency = $6,
		    fiscal_calendar_pattern = $7,
		    financial_year_end = $8,
		    status = $9,
		    fiscal_week_end_day = $10,
		    fiscal_year_end_rule = $11,
		    custom_periods = $12,
		    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + entityColumns,
	Delete:   `DELETE FROM entities WHERE id = $1`,
	Count:    `SELECT COUNT(*)::int AS total FROM entities`,
	ListBase: `SELECT ` + entityColumns + ` FROM entities`,
}

// readScope narrows every read of this repository to the entity subtree the
// caller's grants cover (ADR-0012 B-3). The anchor is the entity's own id, and
// it is deliberately the SUBTREE ONLY: the ancestors above a scope root are not
// added back. The hierarchy still renders — the client treats an entity whose
// parent it cannot resolve as a root of the tree it can see, and the scope
// root's own parentEntityId is still returned, so a scoped reader can tell
// that a parent exists without reading it.
//
// `id` is unqualified because the entities statements join nothing; the base
// repository reuses the same predicate for GetById, where `id` is the projected
// column.
func readScope(db database.ExecerPg) func(context.Context, func(any) string) (string, error) {
	return func(ctx context.Context, bind func(any) string) (string, error) {
		scope, err := authzpg.ReadScope(ctx, db, authz.EntityRead)
		if err != nil {
			return "", err
		}
		return scope.EntityPredicate("id", bind), nil
	}
}
