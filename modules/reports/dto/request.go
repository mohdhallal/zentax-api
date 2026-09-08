package dto

// TaskFilterQuery is the filter set the task feed (/reports/task-instances)
// and the task summary (/reports/task-summary) share, so a tile and the list
// behind it are always cut from the same set. status admits the pseudo-value
// `open` (= not completed); financialYear repeats (`?financialYear=2025&
// financialYear=none`), with `none` standing for workflows that carry no
// financial year — project workflows — so those stay inside a year scope.
type TaskFilterQuery struct {
	WorkflowID       *string  `json:"workflowId"       validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	EntityID         *string  `json:"entityId"         validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	AssigneeID       *string  `json:"assigneeId"       validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	FinancialYear    []string `json:"financialYear"    validate:"omitempty,dive,max=9" example:"2025"`
	Status           *string  `json:"status"           validate:"omitempty,oneof=open not_started in_progress in_review pending_approval completed blocked" example:"open"`
	WorkflowCategory *string  `json:"workflowCategory" validate:"omitempty,oneof=recurring project" example:"recurring"`
}

// ListTaskInstancesQuery filters + pages the enriched task-instance report.
// sort is a single key; the default (dueDate:asc) is applied in the handler
// because slice fields take no `default` tag.
type ListTaskInstancesQuery struct {
	TaskFilterQuery
	Limit  int      `json:"limit"  default:"100" validate:"min=1,max=500" example:"100"`
	Offset int      `json:"offset" default:"0"   validate:"min=0" example:"0"`
	Sort   []string `json:"sort"   validate:"omitempty,dive,oneof=dueDate:asc dueDate:desc createdAt:asc createdAt:desc" example:"dueDate:asc"`
}

// TaskSummaryQuery narrows the task summary: the shared filters, nothing else
// (the summary is one aggregate row — no paging, no sort).
type TaskSummaryQuery struct {
	TaskFilterQuery
}
