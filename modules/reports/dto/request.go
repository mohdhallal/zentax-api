package dto

// TaskFilterQuery is the filter set the task feed (/reports/task-instances)
// and the task summary (/reports/task-summary) share, so a tile and the list
// behind it are always cut from the same set.
//
//   - status admits the pseudo-value `open` (= not completed).
//   - assigneeId is a user id, `me` (the session user) or `unassigned`.
//   - financialYear repeats (`?financialYear=2025&financialYear=none`), with
//     `none` standing for workflows that carry no financial year — project
//     workflows — so those stay inside a year scope; "" and the legacy "all"
//     mean no filter.
//   - taxType is the obligation type's template (VAT / CIT / TP / WHT / Custom).
//   - due is one of the dashboard's windows, evaluated on the server against
//     the tenant's civil day — exactly the predicate the summary tile counts, so
//     a tile drill-down lists precisely the instances it counted; dueFrom /
//     dueTo bound the due date inclusively (YYYY-MM-DD).
//   - search is a literal, case-insensitive substring (LIKE metacharacters are
//     escaped) over the task name, period code, workflow name, entity name and
//     obligation-type name.
type TaskFilterQuery struct {
	WorkflowID       *string  `json:"workflowId"       validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	EntityID         *string  `json:"entityId"         validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	AssigneeID       *string  `json:"assigneeId"       validate:"omitempty,uuid|oneof=me unassigned" example:"me"`
	ObligationTypeID *string  `json:"obligationTypeId" validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	TaxType          *string  `json:"taxType"          validate:"omitempty,oneof=VAT CIT TP WHT Custom" example:"VAT"`
	FinancialYear    []string `json:"financialYear"    validate:"omitempty,dive,max=9" example:"2025"`
	PeriodCode       *string  `json:"periodCode"       validate:"omitempty,max=16" example:"M1"`
	Status           *string  `json:"status"           validate:"omitempty,oneof=open not_started in_progress in_review pending_approval completed blocked" example:"open"`
	WorkflowCategory *string  `json:"workflowCategory" validate:"omitempty,oneof=recurring project" example:"recurring"`
	Due              *string  `json:"due"              validate:"omitempty,oneof=overdue today thisWeek" example:"overdue"`
	DueFrom          *string  `json:"dueFrom"          validate:"omitempty,datetime=2006-01-02" example:"2025-01-01"`
	DueTo            *string  `json:"dueTo"            validate:"omitempty,datetime=2006-01-02" example:"2025-12-31"`
	Search           *string  `json:"search"           validate:"omitempty,max=200" example:"VAT"`
}

// ListTaskInstancesQuery filters + pages the enriched task-instance report.
// sort is a single key — dueDate, createdAt, status (board rank), workflow
// (name), entity (name, instances without one last) or name — each in both
// directions; the default (dueDate:asc) is applied in the handler because
// slice fields take no `default` tag. Every key is followed by the feed's
// natural order and the instance id in the same direction, so pages are stable.
type ListTaskInstancesQuery struct {
	TaskFilterQuery
	Limit  int      `json:"limit"  default:"100" validate:"min=1,max=500" example:"100"`
	Offset int      `json:"offset" default:"0"   validate:"min=0" example:"0"`
	Sort   []string `json:"sort"   validate:"omitempty,max=1,dive,oneof=dueDate:asc dueDate:desc createdAt:asc createdAt:desc status:asc status:desc workflow:asc workflow:desc entity:asc entity:desc name:asc name:desc" example:"dueDate:asc"`
}

// TaskSummaryQuery narrows the task summary: the shared filters, nothing else
// (the summary is one aggregate row — no paging, no sort).
type TaskSummaryQuery struct {
	TaskFilterQuery
}

// WorkflowStatsQuery narrows /reports/workflow-stats to a subset of the
// tenant's workflows (the response shape is unchanged: an object keyed by
// workflow id). financialYear repeats and admits `none` like the task filters;
// status is the workflow's own status.
type WorkflowStatsQuery struct {
	WorkflowID       *string  `json:"workflowId"       validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	EntityID         *string  `json:"entityId"         validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	FinancialYear    []string `json:"financialYear"    validate:"omitempty,dive,max=9" example:"2025"`
	Status           *string  `json:"status"           validate:"omitempty,oneof=draft active completed archived" example:"active"`
	WorkflowCategory *string  `json:"workflowCategory" validate:"omitempty,oneof=recurring project" example:"recurring"`
}
