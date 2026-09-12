package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// auditValues is the ADR-0008 whitelist for a workflow: the fields that decide
// what the workflow files, for which periods, and when it is due — plus its
// lifecycle. It feeds audit.Changes, which records the before and after of
// whatever actually moved.
//
// Recorded verbatim (enums, ids, legal date-onlys, period codes, the rule
// object — no personal data, no free text):
//
//   - status: draft → active → archived is the workflow's lifecycle, and the
//     one field a "why did this stop generating tasks?" question lands on.
//   - dueDateRule: the offset that computes every task instance's due date. An
//     edit silently re-dates future work, so both sides belong in the trail.
//   - periodicity, financialYear, selectedPeriods: which periods exist at all.
//   - workflowCategory / projectType: recurring obligation vs one-off project.
//   - entityId / obligationTypeId: what is being filed, and for whom — also the
//     RBAC scope of everything beneath the workflow (ADR-0012).
//   - startDate / endDate: the window the workflow operates in.
//   - tasksSequential: whether steps must run in order, a control setting.
//
// Redacted — the change is dated, the value withheld: name and description,
// both free text a user typed.
//
// Left out by design: entityName / obligationTypeName (read-model joins, and
// the ids above already identify the rows), the actor / timestamp columns (the
// envelope carries those) and the id (resource_id).
func auditValues(wf *domain.Workflow) audit.Values {
	if wf == nil {
		return nil
	}
	return audit.Values{
		"name":             audit.Redact(wf.Name),
		"description":      audit.Redact(wf.Description),
		"workflowCategory": wf.WorkflowCategory,
		"projectType":      wf.ProjectType,
		"entityId":         wf.EntityID,
		"obligationTypeId": wf.ObligationTypeID,
		"periodicity":      wf.Periodicity,
		"financialYear":    wf.FinancialYear,
		"selectedPeriods":  []string(wf.SelectedPeriods),
		"dueDateRule":      wf.DueDateRule,
		"startDate":        wf.StartDate,
		"endDate":          wf.EndDate,
		"tasksSequential":  wf.TasksSequential,
		"status":           wf.Status,
	}
}
