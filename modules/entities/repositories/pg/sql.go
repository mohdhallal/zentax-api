package pg

import baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"

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
	GetById:          `SELECT ` + entityColumns + ` FROM entities WHERE id = $1 LIMIT 1`,
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
