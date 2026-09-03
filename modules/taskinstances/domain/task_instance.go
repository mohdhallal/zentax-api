package domain

import (
	"time"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

type TaskInstanceID = string

// TaskInstance is an actual per-period task generated from a workflow task
// template when a workflow is started. DueDate / PeriodEndDate / FilingDeadline
// are legal date-only values (ADR-0002). TenantID is RLS infra, absent here.
type TaskInstance struct {
	ID               TaskInstanceID `json:"id"               db:"id"`
	WorkflowID       string         `json:"workflowId"       db:"workflow_id"`
	WorkflowTaskID   string         `json:"workflowTaskId"   db:"workflow_task_id"`
	PeriodCode       string         `json:"periodCode"       db:"period_code"`
	Name             string         `json:"name"             db:"name"`
	Description      *string        `json:"description"      db:"description"`
	TaskType         string         `json:"taskType"         db:"task_type"`
	Status           string         `json:"status"           db:"status"`
	AssigneeID       *string        `json:"assigneeId"       db:"assignee_id"`
	DueDate          dateonly.Date  `json:"dueDate"          db:"due_date"`
	PeriodEndDate    dateonly.Date  `json:"periodEndDate"    db:"period_end_date"`
	FilingDeadline   dateonly.Date  `json:"filingDeadline"   db:"filing_deadline"`
	ApprovalRequired bool           `json:"approvalRequired" db:"approval_required"`
	ApprovedBy       *string        `json:"approvedBy"       db:"approved_by"`
	ApprovedAt       *time.Time     `json:"approvedAt"       db:"approved_at"`
	CompletedAt      *time.Time     `json:"completedAt"      db:"completed_at"`
	SubmittedBy      *string        `json:"submittedBy"      db:"submitted_by"`
	SubmittedAt      *time.Time     `json:"submittedAt"      db:"submitted_at"`
	RejectionReason  *string        `json:"rejectionReason"  db:"rejection_reason"`
	OrderIndex       int            `json:"orderIndex"       db:"order_index"`
	Notes            *string        `json:"notes"            db:"notes"`
	DataTemplateID   *string        `json:"dataTemplateId"   db:"data_template_id"`
	TaxData          TaxData        `json:"taxData"          db:"tax_data"`
	TaxDataStatus    string         `json:"taxDataStatus"    db:"tax_data_status"`
	CreatedBy        *string        `json:"createdBy" db:"created_by"`
	UpdatedBy        *string        `json:"updatedBy" db:"updated_by"`
	CreatedAt        time.Time      `json:"createdAt"        db:"created_at"`
	UpdatedAt        time.Time      `json:"updatedAt"        db:"updated_at"`
}

// CreateTaskInstanceInput is used by workflow-start generation (not a public API).
type CreateTaskInstanceInput struct {
	WorkflowID       string
	WorkflowTaskID   string
	PeriodCode       string
	Name             string
	Description      *string
	TaskType         string
	DueDate          dateonly.Date
	PeriodEndDate    dateonly.Date
	FilingDeadline   dateonly.Date
	ApprovalRequired bool
	OrderIndex       int
	DataTemplateID   *string
}

// UpdateTaskInstanceInput is the API-editable subset. Approval/completion
// timestamps are set by the dedicated submit/approve/reject actions, not here.
type UpdateTaskInstanceInput struct {
	Status        string
	AssigneeID    *string
	DueDate       *dateonly.Date // nil keeps the current due date
	Notes         *string
	TaxData       TaxData
	TaxDataStatus string
}

// Task-instance lifecycle statuses relevant to the approval flow (ADR-0018).
const (
	StatusInProgress      = "in_progress"
	StatusPendingApproval = "pending_approval"
	StatusCompleted       = "completed"
)

// IsApproved reports whether the instance has been approved and is therefore
// immutable (ADR-0018): a change must be a new amendment, not an in-place edit.
func (t *TaskInstance) IsApproved() bool { return t.ApprovedBy != nil }

// IsPendingApproval reports whether the instance is submitted and awaiting review.
func (t *TaskInstance) IsPendingApproval() bool { return t.Status == StatusPendingApproval }
