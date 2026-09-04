package dto

import (
	"time"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
)

const instantLayout = "2006-01-02T15:04:05.000Z"

// TaskInstanceRowToJSON renders one enriched instance. Date-only columns
// (dueDate/periodEndDate/filingDeadline) marshal as YYYY-MM-DD via
// dateonly.Date (ADR-0002); instants are UTC ISO-8601 (ADR-0003).
func TaskInstanceRowToJSON(r *domain.TaskInstanceRow) map[string]any {
	return map[string]any{
		"id":                 r.ID,
		"workflowId":         r.WorkflowID,
		"workflowTaskId":     r.WorkflowTaskID,
		"periodCode":         r.PeriodCode,
		"name":               r.Name,
		"description":        r.Description,
		"taskType":           r.TaskType,
		"status":             r.Status,
		"assigneeId":         r.AssigneeID,
		"assigneeName":       r.AssigneeName,
		"dueDate":            r.DueDate,
		"periodEndDate":      r.PeriodEndDate,
		"filingDeadline":     r.FilingDeadline,
		"approvalRequired":   r.ApprovalRequired,
		"approvedBy":         r.ApprovedBy,
		"approvedAt":         formatInstant(r.ApprovedAt),
		"completedAt":        formatInstant(r.CompletedAt),
		"submittedBy":        r.SubmittedBy,
		"submittedAt":        formatInstant(r.SubmittedAt),
		"rejectionReason":    r.RejectionReason,
		"orderIndex":         r.OrderIndex,
		"notes":              r.Notes,
		"dataTemplateId":     r.DataTemplateID,
		"taxDataStatus":      r.TaxDataStatus,
		"createdAt":          r.CreatedAt.UTC().Format(instantLayout),
		"updatedAt":          r.UpdatedAt.UTC().Format(instantLayout),
		"workflowName":       r.WorkflowName,
		"workflowCategory":   r.WorkflowCategory,
		"projectType":        r.ProjectType,
		"financialYear":      r.FinancialYear,
		"entityId":           r.EntityID,
		"entityName":         r.EntityName,
		"obligationTypeId":   r.ObligationTypeID,
		"obligationTypeName": r.ObligationTypeName,
		"taxType":            r.TaxType,
	}
}

// WorkflowStatsToJSON renders the stats map keyed by workflow id. The map is
// always non-nil so a tenant without workflows serializes as {} — never null.
func WorkflowStatsToJSON(stats []domain.WorkflowStats) map[string]any {
	out := make(map[string]any, len(stats))
	for i := range stats {
		s := &stats[i]
		var next any
		if !s.NextDueDate.IsZero() {
			next = s.NextDueDate
		}
		out[s.WorkflowID] = map[string]any{
			"totalTasks":        s.TotalTasks,
			"completedTasks":    s.CompletedTasks,
			"completionPercent": s.CompletionPercent(),
			"nextDueDate":       next,
		}
	}
	return out
}

func formatInstant(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(instantLayout)
}
