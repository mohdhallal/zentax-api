package dto

import "github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"

type UpdateTaskInstanceBody struct {
	Status        string         `json:"status"        validate:"required,oneof=not_started in_progress in_review completed blocked"`
	AssigneeID    *string        `json:"assigneeId"    validate:"omitempty,uuid"`
	Notes         *string        `json:"notes"         validate:"omitempty,max=5000"`
	TaxData       domain.TaxData `json:"taxData"       validate:"omitempty"`
	TaxDataStatus string         `json:"taxDataStatus" validate:"omitempty,oneof=draft final"`
}

type TaskInstanceIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

// RejectTaskInstanceBody carries an optional reviewer reason for a rejection.
type RejectTaskInstanceBody struct {
	Reason *string `json:"reason" validate:"omitempty,max=2000"`
}

type ListTaskInstancesQuery struct {
	Limit      int      `json:"limit"      default:"20" validate:"min=1,max=100" example:"20"`
	Offset     int      `json:"offset"     default:"0"  validate:"min=0" example:"0"`
	Sort       []string `json:"sort"       validate:"omitempty,dive,oneof=dueDate:asc dueDate:desc createdAt:asc createdAt:desc" example:"dueDate:asc"`
	WorkflowID *string  `json:"workflowId" filter:"workflow_id" validate:"omitempty,uuid"`
	Status     *string  `json:"status"     filter:"status" validate:"omitempty,oneof=not_started in_progress in_review completed blocked" example:"not_started"`
	PeriodCode *string  `json:"periodCode" filter:"period_code" validate:"omitempty,max=10" example:"M1"`
}
