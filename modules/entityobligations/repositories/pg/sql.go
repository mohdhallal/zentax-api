package pg

import baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"

// entityObligationColumns is the domain projection — WITHOUT tenant_id (RLS
// infra, absent from the model). deadline_rule (JSONB) scans into the
// domain.DeadlineRule Scanner/Valuer type.
const entityObligationColumns = `id, entity_id, obligation_type_id, jurisdiction, periodicity, ` +
	`deadline_rule, status, created_at, updated_at, created_by, updated_by`

var sqlConfig = baserepo.SQLConfig{
	AllowedColumns: map[string]bool{
		"created_at":         true,
		"entity_id":          true,
		"obligation_type_id": true,
		"periodicity":        true,
		"status":             true,
		"jurisdiction":       true,
	},
	DefaultOrderBy:   "created_at",
	DefaultOrderDesc: true, // newest first
	GetById:          `SELECT ` + entityObligationColumns + ` FROM entity_obligations WHERE id = $1 LIMIT 1`,
	// tenant_id defaults from the GUC; status/deadline_rule default in DDL.
	Create: `
		INSERT INTO entity_obligations (entity_id, obligation_type_id, jurisdiction, periodicity, deadline_rule)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + entityObligationColumns,
	Update: `
		UPDATE entity_obligations
		SET jurisdiction = $2,
		    periodicity = $3,
		    deadline_rule = $4,
		    status = $5,
		    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + entityObligationColumns,
	Delete:   `DELETE FROM entity_obligations WHERE id = $1`,
	Count:    `SELECT COUNT(*)::int AS total FROM entity_obligations`,
	ListBase: `SELECT ` + entityObligationColumns + ` FROM entity_obligations`,
}
