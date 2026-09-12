package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/platform/authz"
	authzpg "github.com/mohamadhallal/zentax-api/platform/authz/pg"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

// taskInstanceColumns is the domain projection — WITHOUT tenant_id (RLS infra).
// due_date/period_end_date/filing_deadline/payment_deadline (DATE) scan into
// dateonly.Date; tax_data (JSONB) into domain.TaxData.
const taskInstanceColumns = `id, workflow_id, workflow_task_id, period_code, name, description, task_type, ` +
	`status, assignee_id, due_date, period_end_date, filing_deadline, payment_deadline, approval_required, approved_by, ` +
	`approved_at, completed_at, submitted_by, submitted_at, rejection_reason, order_index, notes, ` +
	`data_template_id, tax_data, tax_data_status, created_at, updated_at, created_by, updated_by`

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
			due_date, period_end_date, filing_deadline, approval_required, order_index, data_template_id,
			payment_deadline
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING ` + taskInstanceColumns,
	Update: `
		UPDATE task_instances
		SET status = $2,
		    assignee_id = $3,
		    notes = $4,
		    tax_data = $5,
		    tax_data_status = $6,
		    due_date = COALESCE($7, due_date),
		    data_template_id = COALESCE($8, data_template_id),
		    completed_at = CASE WHEN $2::varchar = 'completed' THEN COALESCE(completed_at, NOW()) ELSE NULL END,
		    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + taskInstanceColumns,
	Delete:   `DELETE FROM task_instances WHERE id = $1`,
	Count:    `SELECT COUNT(*)::int AS total FROM task_instances`,
	ListBase: `SELECT ` + taskInstanceColumns + ` FROM task_instances`,
}

// readScope narrows every read of this repository to the caller's entity
// subtree (ADR-0012 B-3). An instance reaches its entity through its workflow,
// so the predicate is an EXISTS on that workflow. It applies to the reads the
// approval flow makes too (GetById before submit / approve / reject): a scoped
// principal is refused by the authorizer there in any case, and this makes the
// refusal independent of the order of the two checks.
func readScope(db database.ExecerPg) func(context.Context, func(any) string) (string, error) {
	return func(ctx context.Context, bind func(any) string) (string, error) {
		scope, err := authzpg.ReadScope(ctx, db, authz.TaskRead)
		if err != nil {
			return "", err
		}
		return scope.WorkflowPredicate("workflow_id", bind), nil
	}
}

// Approval-flow transitions (ADR-0018). Preconditions + SoD are enforced in the
// use cases; these just write the state change and return the updated row.
const submitForApprovalSQL = `
	UPDATE task_instances
	SET status = 'pending_approval', submitted_by = $2, submitted_at = NOW(),
	    rejection_reason = NULL, updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid, updated_at = NOW()
	WHERE id = $1
	RETURNING ` + taskInstanceColumns

const approveSQL = `
	UPDATE task_instances
	SET status = 'completed', approved_by = $2, approved_at = NOW(), completed_at = NOW(),
	    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
	    updated_at = NOW()
	WHERE id = $1
	RETURNING ` + taskInstanceColumns

const rejectSQL = `
	UPDATE task_instances
	SET status = 'in_progress', submitted_by = NULL, submitted_at = NULL,
	    rejection_reason = $2, updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid, updated_at = NOW()
	WHERE id = $1
	RETURNING ` + taskInstanceColumns
