package dto

import "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"

type CreateWorkflowTaskBody struct {
	WorkflowID             string                      `json:"workflowId"             validate:"required,uuid"`
	Name                   string                      `json:"name"                   validate:"required,min=1,max=200"`
	Description            *string                     `json:"description"            validate:"omitempty,max=2000"`
	TaskType               string                      `json:"taskType"               validate:"required,oneof=data_request review preparation submission payment approval other"`
	RoleLabel              *string                     `json:"roleLabel"              validate:"omitempty,max=100"`
	ApprovalRequired       bool                        `json:"approvalRequired"`
	DueDateReference       string                      `json:"dueDateReference"       validate:"omitempty,oneof=filing_deadline period_end"`
	DueDateOffsetValue     int                         `json:"dueDateOffsetValue"     validate:"min=0,max=365"`
	DueDateOffsetUnit      string                      `json:"dueDateOffsetUnit"      validate:"omitempty,oneof=days weeks months"`
	DueDateOffsetDirection string                      `json:"dueDateOffsetDirection" validate:"omitempty,oneof=before after"`
	OrderIndex             int                         `json:"orderIndex"             validate:"min=0"`
	DataTemplateID         *string                     `json:"dataTemplateId"         validate:"omitempty,uuid"`
	RequiredDocuments      domain.DocumentRequirements `json:"requiredDocuments"      validate:"omitempty,dive"`
}

type UpdateWorkflowTaskBody struct {
	Name                   string                      `json:"name"                   validate:"required,min=1,max=200"`
	Description            *string                     `json:"description"            validate:"omitempty,max=2000"`
	TaskType               string                      `json:"taskType"               validate:"required,oneof=data_request review preparation submission payment approval other"`
	RoleLabel              *string                     `json:"roleLabel"              validate:"omitempty,max=100"`
	ApprovalRequired       bool                        `json:"approvalRequired"`
	DueDateReference       string                      `json:"dueDateReference"       validate:"omitempty,oneof=filing_deadline period_end"`
	DueDateOffsetValue     int                         `json:"dueDateOffsetValue"     validate:"min=0,max=365"`
	DueDateOffsetUnit      string                      `json:"dueDateOffsetUnit"      validate:"omitempty,oneof=days weeks months"`
	DueDateOffsetDirection string                      `json:"dueDateOffsetDirection" validate:"omitempty,oneof=before after"`
	OrderIndex             int                         `json:"orderIndex"             validate:"min=0"`
	DataTemplateID         *string                     `json:"dataTemplateId"         validate:"omitempty,uuid"`
	RequiredDocuments      domain.DocumentRequirements `json:"requiredDocuments"      validate:"omitempty,dive"`
}

type WorkflowTaskIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

type ListWorkflowTasksQuery struct {
	Limit      int      `json:"limit"      default:"20" validate:"min=1,max=100" example:"20"`
	Offset     int      `json:"offset"     default:"0"  validate:"min=0" example:"0"`
	Sort       []string `json:"sort"       validate:"omitempty,dive,oneof=orderIndex:asc orderIndex:desc createdAt:asc createdAt:desc" example:"orderIndex:asc"`
	WorkflowID *string  `json:"workflowId" filter:"workflow_id" validate:"omitempty,uuid"`
	TaskType   *string  `json:"taskType"   filter:"task_type" validate:"omitempty,oneof=data_request review preparation submission payment approval other"`
}
