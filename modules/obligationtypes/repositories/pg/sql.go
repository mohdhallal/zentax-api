package pg

import baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"

// obligationTypeColumns is the domain projection — WITHOUT tenant_id (RLS infra,
// absent from the model, would break sqlx struct scanning).
const obligationTypeColumns = `id, name, code, category, template, status, description, created_at, updated_at, created_by, updated_by`

var sqlConfig = baserepo.SQLConfig{
	AllowedColumns: map[string]bool{
		"created_at": true,
		"name":       true,
		"code":       true,
		"category":   true,
		"template":   true,
		"status":     true,
	},
	DefaultOrderBy:   "created_at",
	DefaultOrderDesc: true, // newest first
	// `search` matches the name or the code (ILIKE, escaped).
	SearchColumns: []string{"name", "code"},
	GetById:       `SELECT ` + obligationTypeColumns + ` FROM obligation_types WHERE id = $1 LIMIT 1`,
	// tenant_id defaults from the app.tenant_id GUC; status/created_at default in DDL.
	Create: `
		INSERT INTO obligation_types (name, code, category, template, description)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + obligationTypeColumns,
	Update: `
		UPDATE obligation_types
		SET name = $2,
		    code = $3,
		    category = $4,
		    template = $5,
		    status = $6,
		    description = $7,
		    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + obligationTypeColumns,
	Delete:   `DELETE FROM obligation_types WHERE id = $1`,
	Count:    `SELECT COUNT(*)::int AS total FROM obligation_types`,
	ListBase: `SELECT ` + obligationTypeColumns + ` FROM obligation_types`,
}
