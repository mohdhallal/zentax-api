package dto

import (
	"time"

	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
)

// TaskInstanceToJSON renders a task instance. dueDate/periodEndDate/filingDeadline
// are legal date-only values (ADR-0002) — dateonly.Date marshals to YYYY-MM-DD,
// never through a timezone. approvedAt/completedAt/created/updated are UTC
// instants (ADR-0003).
func TaskInstanceToJSON(ti *domain.TaskInstance) map[string]any {
	return map[string]any{
		"id":               ti.ID,
		"workflowId":       ti.WorkflowID,
		"workflowTaskId":   ti.WorkflowTaskID,
		"periodCode":       ti.PeriodCode,
		"name":             ti.Name,
		"description":      ti.Description,
		"taskType":         ti.TaskType,
		"status":           ti.Status,
		"assigneeId":       ti.AssigneeID,
		"dueDate":          ti.DueDate,
		"periodEndDate":    ti.PeriodEndDate,
		"filingDeadline":   ti.FilingDeadline,
		"approvalRequired": ti.ApprovalRequired,
		"approvedBy":       ti.ApprovedBy,
		"approvedAt":       formatInstant(ti.ApprovedAt),
		"completedAt":      formatInstant(ti.CompletedAt),
		"orderIndex":       ti.OrderIndex,
		"notes":            ti.Notes,
		"dataTemplateId":   ti.DataTemplateID,
		"taxData":          ti.TaxData,
		"taxDataStatus":    ti.TaxDataStatus,
		"createdAt":        ti.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"updatedAt":        ti.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

func formatInstant(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
