package domain

import "time"

type WorkflowTaskID = string

// WorkflowTask is an ordered task template on a workflow. WorkflowID is fixed at
// creation. TenantID is infrastructure (RLS), absent from the model.
type WorkflowTask struct {
	ID                     WorkflowTaskID       `json:"id"                     db:"id"`
	WorkflowID             string               `json:"workflowId"             db:"workflow_id"`
	Name                   string               `json:"name"                   db:"name"`
	Description            *string              `json:"description"            db:"description"`
	TaskType               string               `json:"taskType"               db:"task_type"`
	RoleLabel              *string              `json:"roleLabel"              db:"role_label"`
	ApprovalRequired       bool                 `json:"approvalRequired"       db:"approval_required"`
	DueDateReference       string               `json:"dueDateReference"       db:"due_date_reference"`
	DueDateOffsetValue     int                  `json:"dueDateOffsetValue"     db:"due_date_offset_value"`
	DueDateOffsetUnit      string               `json:"dueDateOffsetUnit"      db:"due_date_offset_unit"`
	DueDateOffsetDirection string               `json:"dueDateOffsetDirection" db:"due_date_offset_direction"`
	OrderIndex             int                  `json:"orderIndex"             db:"order_index"`
	DataTemplateID         *string              `json:"dataTemplateId"         db:"data_template_id"`
	RequiredDocuments      DocumentRequirements `json:"requiredDocuments"      db:"required_documents"`
	CreatedBy              *string              `json:"createdBy" db:"created_by"`
	UpdatedBy              *string              `json:"updatedBy" db:"updated_by"`
	CreatedAt              time.Time            `json:"createdAt"              db:"created_at"`
	UpdatedAt              time.Time            `json:"updatedAt"              db:"updated_at"`
}

type CreateWorkflowTaskInput struct {
	WorkflowID             string
	Name                   string
	Description            *string
	TaskType               string
	RoleLabel              *string
	ApprovalRequired       bool
	DueDateReference       string
	DueDateOffsetValue     int
	DueDateOffsetUnit      string
	DueDateOffsetDirection string
	OrderIndex             int
	DataTemplateID         *string
	RequiredDocuments      DocumentRequirements
}

type UpdateWorkflowTaskInput struct {
	Name                   string
	Description            *string
	TaskType               string
	RoleLabel              *string
	ApprovalRequired       bool
	DueDateReference       string
	DueDateOffsetValue     int
	DueDateOffsetUnit      string
	DueDateOffsetDirection string
	OrderIndex             int
	DataTemplateID         *string
	RequiredDocuments      DocumentRequirements
}
