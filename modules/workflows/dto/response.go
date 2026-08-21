package dto

import "github.com/mohamadhallal/zentax-api/modules/workflows/domain"

// WorkflowToJSON renders a workflow. startDate/endDate are legal date-only
// strings carried verbatim (ADR-0002); createdAt/updatedAt are UTC instants
// (ADR-0003).
func WorkflowToJSON(wf *domain.Workflow) map[string]any {
	return map[string]any{
		"id":               wf.ID,
		"name":             wf.Name,
		"description":      wf.Description,
		"workflowCategory": wf.WorkflowCategory,
		"projectType":      wf.ProjectType,
		"financialYear":    wf.FinancialYear,
		"periodicity":      wf.Periodicity,
		"selectedPeriods":  wf.SelectedPeriods,
		"entityId":         wf.EntityID,
		"obligationTypeId": wf.ObligationTypeID,
		"dueDateRule":      wf.DueDateRule,
		"startDate":        wf.StartDate,
		"endDate":          wf.EndDate,
		"tasksSequential":  wf.TasksSequential,
		"status":           wf.Status,
		"createdAt":        wf.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"updatedAt":        wf.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}
