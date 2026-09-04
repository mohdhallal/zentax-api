package dto

// ListTaskInstancesQuery filters + pages the enriched task-instance report.
// sort is a single key; the default (dueDate:asc) is applied in the handler
// because slice fields take no `default` tag.
type ListTaskInstancesQuery struct {
	Limit      int      `json:"limit"      default:"100" validate:"min=1,max=500" example:"100"`
	Offset     int      `json:"offset"     default:"0"   validate:"min=0" example:"0"`
	Sort       []string `json:"sort"       validate:"omitempty,dive,oneof=dueDate:asc dueDate:desc createdAt:asc createdAt:desc" example:"dueDate:asc"`
	WorkflowID *string  `json:"workflowId" validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	EntityID   *string  `json:"entityId"   validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	Status     *string  `json:"status"     validate:"omitempty,oneof=not_started in_progress in_review pending_approval completed blocked" example:"in_progress"`
}
