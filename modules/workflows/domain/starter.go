package domain

import (
	"context"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// Starter plans and generates task instances for a workflow
// (GET /workflows/{id}/preview, POST /workflows/{id}/start). It is implemented
// by the task-instances generator and injected in bootstrap; declared here so
// the workflows module does not depend on task-instances.
//
// Preview and start share one planning function: what preview shows is exactly
// what start persists (modulo the caller's overrides).
type Starter interface {
	// PreviewWorkflow computes the instances start would create, without
	// creating anything (pure read; no audit entry; works whether or not the
	// workflow has already been started).
	PreviewWorkflow(ctx context.Context, workflowID string) (*WorkflowPreview, error)
	// StartWorkflow materializes the planned instances (after applying the
	// per-instance overrides) and returns the count created.
	StartWorkflow(ctx context.Context, workflowID string, overrides TaskOverrides) (int, error)
}

// TaskOverride adjusts one planned instance, identified by
// "<templateId>_<periodCode>". A PeriodEndDate override replaces that instance's
// period end and its filing deadline / due date are recomputed from it (unless
// DueDate is also overridden); a DueDate override replaces the due date only.
type TaskOverride struct {
	DueDate       *dateonly.Date
	PeriodEndDate *dateonly.Date
}

// TaskOverrides maps "<templateId>_<periodCode>" to its override.
type TaskOverrides map[string]TaskOverride

// OverrideKey builds the TaskOverrides key for a (template, period) pair.
func OverrideKey(templateID, periodCode string) string {
	return templateID + "_" + periodCode
}

// PreviewTask is one planned task instance (dates are legal date-only values,
// ADR-0002).
type PreviewTask struct {
	TemplateID     string        `json:"templateId"`
	PeriodCode     string        `json:"periodCode"`
	Name           string        `json:"name"`
	TaskType       string        `json:"taskType"`
	AssigneeName   *string       `json:"assigneeName"`
	DueDate        dateonly.Date `json:"dueDate"`
	PeriodEndDate  dateonly.Date `json:"periodEndDate"`
	FilingDeadline dateonly.Date `json:"filingDeadline"`
	OrderIndex     int           `json:"orderIndex"`
}

// WorkflowPreview summarizes what starting the workflow would create. Tasks are
// ordered by the workflow's selectedPeriods order (not lexicographically), then
// by template orderIndex.
type WorkflowPreview struct {
	WorkflowID        string        `json:"workflowId"`
	WorkflowName      string        `json:"workflowName"`
	TotalPeriods      int           `json:"totalPeriods"`
	TaskTemplates     int           `json:"taskTemplates"`
	TotalTasks        int           `json:"totalTasks"`
	AssigneesImpacted []string      `json:"assigneesImpacted"`
	Tasks             []PreviewTask `json:"tasks"`
}
