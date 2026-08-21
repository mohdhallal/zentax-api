package pg

import baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"

// taskInstanceColumns is the domain projection — WITHOUT tenant_id (RLS infra).
// due_date/period_end_date/filing_deadline (DATE) scan into dateonly.Date;
// tax_data (JSONB) into domain.TaxData.
const taskInstanceColumns = `id, workflow_id, workflow_task_id, period_code, name, description, task_type, ` +
	`status, assignee_id, due_date, period_end_date, filing_deadline, approval_required, approved_by, ` +
	`approved_at, completed_at, order_index, notes, data_template_id, tax_data, tax_data_status, ` +
	`created_at, updated_at`

var sqlConfig = baserepo.SQLConfig{
	AllowedColumns: map[string]bool{
		"due_date":         true,
		"created_at":       true,
		"workflow_id":      true,
		"workflow_task_id": true,
		"status":           true,
		"period_code":      true,
		"assignee_id":      true,
		"order_index":      true,
	},
	DefaultOrderBy: "due_date",
	GetById:        `SELECT ` + taskInstanceColumns + ` FROM task_instances WHERE id = $1 LIMIT 1`,
	// status/tax_data_status default in DDL; tenant_id from the GUC.
	Create: `
		INSERT INTO task_instances (
			workflow_id, workflow_task_id, period_code, name, description, task_type,
			due_date, period_end_date, filing_deadline, approval_required, order_index, data_template_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING ` + taskInstanceColumns,
	Update: `
		UPDATE task_instances
		SET status = $2,
		    assignee_id = $3,
		    notes = $4,
		    tax_data = $5,
		    tax_data_status = $6,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + taskInstanceColumns,
	Delete:   `DELETE FROM task_instances WHERE id = $1`,
	Count:    `SELECT COUNT(*)::int AS total FROM task_instances`,
	ListBase: `SELECT ` + taskInstanceColumns + ` FROM task_instances`,
}
