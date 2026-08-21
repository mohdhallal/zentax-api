package pg

import baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"

// workflowColumns is the domain projection — WITHOUT tenant_id (RLS infra).
// selected_periods (JSONB) and due_date_rule (JSONB) scan into the domain
// Periods / DueDateRule Scanner/Valuer types.
const workflowColumns = `id, name, description, workflow_category, project_type, financial_year, ` +
	`periodicity, selected_periods, entity_id, obligation_type_id, due_date_rule, start_date, end_date, ` +
	`tasks_sequential, status, created_at, updated_at`

var sqlConfig = baserepo.SQLConfig{
	AllowedColumns: map[string]bool{
		"created_at":         true,
		"name":               true,
		"workflow_category":  true,
		"status":             true,
		"entity_id":          true,
		"obligation_type_id": true,
		"financial_year":     true,
	},
	DefaultOrderBy: "created_at",
	GetById:        `SELECT ` + workflowColumns + ` FROM workflows WHERE id = $1 LIMIT 1`,
	// tenant_id defaults from the GUC; selected_periods/due_date_rule/status default in DDL.
	Create: `
		INSERT INTO workflows (
			name, description, workflow_category, project_type, financial_year, periodicity,
			selected_periods, entity_id, obligation_type_id, due_date_rule, start_date, end_date, tasks_sequential
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING ` + workflowColumns,
	Update: `
		UPDATE workflows
		SET name = $2,
		    description = $3,
		    workflow_category = $4,
		    project_type = $5,
		    financial_year = $6,
		    periodicity = $7,
		    selected_periods = $8,
		    entity_id = $9,
		    obligation_type_id = $10,
		    due_date_rule = $11,
		    start_date = $12,
		    end_date = $13,
		    tasks_sequential = $14,
		    status = $15,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + workflowColumns,
	Delete:   `DELETE FROM workflows WHERE id = $1`,
	Count:    `SELECT COUNT(*)::int AS total FROM workflows`,
	ListBase: `SELECT ` + workflowColumns + ` FROM workflows`,
}
