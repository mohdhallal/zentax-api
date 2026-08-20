package pg

import baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"

// entityColumns is the domain projection — deliberately WITHOUT tenant_id, which
// is infrastructure (RLS-enforced) and absent from the domain.Entity struct, so
// selecting it would break sqlx struct scanning.
const entityColumns = `id, parent_entity_id, name, legal_name, country, tax_residency, ` +
	`fiscal_calendar_pattern, financial_year_end, status, created_at, updated_at`

var sqlConfig = baserepo.SQLConfig{
	AllowedColumns: map[string]bool{
		"created_at":       true,
		"name":             true,
		"country":          true,
		"status":           true,
		"parent_entity_id": true,
	},
	DefaultOrderBy: "created_at",
	GetById:        `SELECT ` + entityColumns + ` FROM entities WHERE id = $1 LIMIT 1`,
	// tenant_id is omitted on purpose: it defaults from the app.tenant_id GUC.
	Create: `
		INSERT INTO entities (parent_entity_id, name, legal_name, country, tax_residency, fiscal_calendar_pattern, financial_year_end)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
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
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + entityColumns,
	Delete:   `DELETE FROM entities WHERE id = $1`,
	Count:    `SELECT COUNT(*)::int AS total FROM entities`,
	ListBase: `SELECT ` + entityColumns + ` FROM entities`,
}
