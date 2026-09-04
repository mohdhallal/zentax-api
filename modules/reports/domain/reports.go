// Package domain holds the read models behind /reports: enriched task
// instances (instance + workflow + entity + obligation type + assignee name)
// and per-workflow completion stats. Read-only — nothing here mutates state.
package domain

import (
	"context"
	"time"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// TaskInstanceRow is one enriched task instance: the task_instances row joined
// to its workflow, the workflow's entity and obligation type (both LEFT — a
// project workflow may have neither) and the assignee's display name (resolved
// at read time from the user row, ADR-0008). Date columns are legal date-only
// values (ADR-0002); instants are UTC (ADR-0003). tenant_id is RLS infra and
// deliberately absent.
type TaskInstanceRow struct {
	ID               string        `db:"id"`
	WorkflowID       string        `db:"workflow_id"`
	WorkflowTaskID   string        `db:"workflow_task_id"`
	PeriodCode       string        `db:"period_code"`
	Name             string        `db:"name"`
	Description      *string       `db:"description"`
	TaskType         string        `db:"task_type"`
	Status           string        `db:"status"`
	AssigneeID       *string       `db:"assignee_id"`
	AssigneeName     *string       `db:"assignee_name"`
	DueDate          dateonly.Date `db:"due_date"`
	PeriodEndDate    dateonly.Date `db:"period_end_date"`
	FilingDeadline   dateonly.Date `db:"filing_deadline"`
	ApprovalRequired bool          `db:"approval_required"`
	ApprovedBy       *string       `db:"approved_by"`
	ApprovedAt       *time.Time    `db:"approved_at"`
	CompletedAt      *time.Time    `db:"completed_at"`
	SubmittedBy      *string       `db:"submitted_by"`
	SubmittedAt      *time.Time    `db:"submitted_at"`
	RejectionReason  *string       `db:"rejection_reason"`
	OrderIndex       int           `db:"order_index"`
	Notes            *string       `db:"notes"`
	DataTemplateID   *string       `db:"data_template_id"`
	TaxDataStatus    string        `db:"tax_data_status"`
	CreatedAt        time.Time     `db:"created_at"`
	UpdatedAt        time.Time     `db:"updated_at"`

	WorkflowName       string  `db:"workflow_name"`
	WorkflowCategory   string  `db:"workflow_category"`
	ProjectType        *string `db:"project_type"`
	FinancialYear      *string `db:"financial_year"`
	EntityID           *string `db:"entity_id"`
	EntityName         *string `db:"entity_name"`
	ObligationTypeID   *string `db:"obligation_type_id"`
	ObligationTypeName *string `db:"obligation_type_name"`
	TaxType            *string `db:"tax_type"` // obligation_types.template (VAT/CIT/...)
}

// Sort columns accepted by ListTaskInstances. Kept as an allow-list so the
// repository never interpolates a caller-controlled column name.
const (
	SortByDueDate   = "due_date"
	SortByCreatedAt = "created_at"
)

// ListTaskInstancesArgs are the validated filters + paging for the enriched
// task-instance list. nil filters mean "any". SortColumn must be one of the
// Sort* constants (the repository falls back to due_date otherwise).
type ListTaskInstancesArgs struct {
	WorkflowID *string
	EntityID   *string
	Status     *string
	SortColumn string
	SortDesc   bool
	Limit      int
	Offset     int
}

// WorkflowStats is the per-workflow completion summary. NextDueDate is the
// earliest due date among the workflow's not-yet-completed instances (zero when
// there is none — rendered as null).
type WorkflowStats struct {
	WorkflowID     string        `db:"workflow_id"`
	TotalTasks     int           `db:"total_tasks"`
	CompletedTasks int           `db:"completed_tasks"`
	NextDueDate    dateonly.Date `db:"next_due_date"`
}

// CompletionPercent is completed/total rounded to the nearest integer, 0 when
// the workflow has no instances.
func (s WorkflowStats) CompletionPercent() int {
	if s.TotalTasks <= 0 {
		return 0
	}
	// Integer rounding without float drift: (2*c*100 + t) / (2*t).
	return (2*s.CompletedTasks*100 + s.TotalTasks) / (2 * s.TotalTasks)
}

// Reader is the read-side port implemented by the Postgres repository. Every
// query runs on the request transaction, so RLS confines it to the session
// tenant (ADR-0004) — implementations never filter by tenant_id themselves.
type Reader interface {
	// ListTaskInstances returns one page of enriched instances plus the total
	// number of rows matching the filters.
	ListTaskInstances(ctx context.Context, args ListTaskInstancesArgs) ([]TaskInstanceRow, int, error)
	// WorkflowStats returns one entry per workflow in the tenant, including
	// workflows with no instances yet.
	WorkflowStats(ctx context.Context) ([]WorkflowStats, error)
}
