package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/platform/authz"
	authzpg "github.com/mohamadhallal/zentax-api/platform/authz/pg"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

// workflowTaskColumns is the domain projection — WITHOUT tenant_id (RLS infra).
// required_documents (JSONB) scans into the domain.DocumentRequirements type.
const workflowTaskColumns = `id, workflow_id, name, description, task_type, role_label, approval_required, ` +
	`due_date_reference, due_date_offset_value, due_date_offset_unit, due_date_offset_direction, ` +
	`order_index, data_template_id, required_documents, created_at, updated_at, created_by, updated_by`

var sqlConfig = baserepo.SQLConfig{
	AllowedColumns: map[string]bool{
		"created_at":  true,
		"workflow_id": true,
		"name":        true,
		"task_type":   true,
		"order_index": true,
	},
	DefaultOrderBy: "order_index",
	GetById:        `SELECT ` + workflowTaskColumns + ` FROM workflow_tasks WHERE id = $1 LIMIT 1`,
	Create: `
		INSERT INTO workflow_tasks (
			workflow_id, name, description, task_type, role_label, approval_required,
			due_date_reference, due_date_offset_value, due_date_offset_unit, due_date_offset_direction,
			order_index, data_template_id, required_documents
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING ` + workflowTaskColumns,
	Update: `
		UPDATE workflow_tasks
		SET name = $2,
		    description = $3,
		    task_type = $4,
		    role_label = $5,
		    approval_required = $6,
		    due_date_reference = $7,
		    due_date_offset_value = $8,
		    due_date_offset_unit = $9,
		    due_date_offset_direction = $10,
		    order_index = $11,
		    data_template_id = $12,
		    required_documents = $13,
		    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + workflowTaskColumns,
	Delete:   `DELETE FROM workflow_tasks WHERE id = $1`,
	Count:    `SELECT COUNT(*)::int AS total FROM workflow_tasks`,
	ListBase: `SELECT ` + workflowTaskColumns + ` FROM workflow_tasks`,
}

// readScope narrows every read of this repository to the caller's entity
// subtree (ADR-0012 B-3). A task template carries no entity of its own — it
// reaches one through its workflow — so the predicate is an EXISTS on that
// workflow, which leaves the row count, order and any aggregate untouched.
func readScope(db database.ExecerPg) func(context.Context, func(any) string) (string, error) {
	return func(ctx context.Context, bind func(any) string) (string, error) {
		scope, err := authzpg.ReadScope(ctx, db, authz.WorkflowTaskRead)
		if err != nil {
			return "", err
		}
		return scope.WorkflowPredicate("workflow_id", bind), nil
	}
}
