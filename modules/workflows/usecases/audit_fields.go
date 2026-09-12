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
// A value is quoted verbatim ONLY when the request contract constrains its
// SHAPE — a `oneof` enum, a fixed-length legal date, a uuid, a rule object of
// enums and integers. A length bound is not a shape, which is why the two
// period fields below go through a gate rather than straight into the envelope:
//
//   - status: draft → active → archived is the workflow's lifecycle, and the
//     one field a "why did this stop generating tasks?" question lands on.
//   - dueDateRule: the offset that computes every task instance's due date. An
//     edit silently re-dates future work, so both sides belong in the trail.
//   - periodicity: `oneof weekly monthly quarterly bi-annual annual
//     consolidated-annual`. Which periods exist at all.
//   - workflowCategory / projectType: recurring obligation vs one-off project.
//     Both `oneof`.
//   - entityId / obligationTypeId: what is being filed, and for whom — also the
//     RBAC scope of everything beneath the workflow (ADR-0012). Both `uuid`.
//   - startDate / endDate: the window the workflow operates in, both `len=10`.
//   - tasksSequential: whether steps must run in order, a control setting.
//
// Shape-gated — quoted when it looks like a code, "redacted" otherwise
// (auditIdentifier):
//
//   - selectedPeriods: the join key to the entity's recorded fiscal calendar.
//     Its contract is `dive,max=16` and nothing else — deliberately the same
//     bound as entities.customPeriods[].code, whose own envelope already gates
//     it (entities/usecases/audit_fields.go:auditPeriods). Quoting it here while
//     gating it there put the same tenant-typed string in the chain through one
//     door and withheld it through the other; sixteen characters is wide enough
//     for an email address, and both doors are now the same door.
//   - financialYear: `max=9`, which fits "2026", "FY26/27" — and "j@doe.com".
//     Gating rather than dropping it keeps the year the periods belong to
//     legible, which is what makes the recorded period codes mean anything.
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
		"financialYear":    auditFinancialYear(wf.FinancialYear),
		"selectedPeriods":  auditPeriods(wf.SelectedPeriods),
		"dueDateRule":      wf.DueDateRule,
		"startDate":        wf.StartDate,
		"endDate":          wf.EndDate,
		"tasksSequential":  wf.TasksSequential,
		"status":           wf.Status,
	}
}

// auditFinancialYear gates the fiscal year the workflow files for. Unset and
// cleared stay themselves: a project workflow carries no year, and clearing one
// is an absence, not a withheld value — only a year that is actually there and
// is not code-shaped becomes "redacted".
func auditFinancialYear(fy *string) any {
	if fy == nil {
		return nil
	}
	if *fy == "" {
		return ""
	}
	return auditIdentifier(*fy, maxFinancialYearLen)
}

// auditPeriods gates each selected period code, preserving order: the order is
// the order the generator walks, so a reordering is a real change and shows up
// as one. An empty selection encodes as null, which audit.Changes drops from a
// one-sided envelope.
func auditPeriods(periods domain.Periods) []string {
	if len(periods) == 0 {
		return nil
	}
	out := make([]string, 0, len(periods))
	for _, p := range periods {
		out = append(out, auditIdentifier(p, maxPeriodCodeLen))
	}
	return out
}

const (
	// maxPeriodCodeLen is dto.CreateWorkflowBody.SelectedPeriods' own
	// `dive,max=16`.
	maxPeriodCodeLen = 16
	// maxFinancialYearLen is dto.CreateWorkflowBody.FinancialYear's own
	// `max=9`.
	maxFinancialYearLen = 9
)

// redactedIdentifier stands in for a string that should have been a code but is
// not shaped like one.
const redactedIdentifier = "redacted"

// auditIdentifier is the gate on the two user-supplied strings this envelope
// quotes. What is guaranteed here is SHAPE, not meaning: a value is quoted only
// if it is built from letters, digits, '_', '-' and '.', which passes every
// period code and fiscal year a real calendar uses ("Q4-adj", "M1", "FY26.13",
// "2026", "2026-27") and redacts anything carrying a space or an '@'.
//
// This is a deliberate per-module copy of one rule — the same gate guards
// custom fiscal period codes (entities), data-template field ids
// (datatemplates), tax-data keys (taskinstances) and additional-deadline types
// (entityobligations). Each module owns its own whitelist and its own length
// bounds; this is the first caller that needs TWO of them, which is the
// argument for lifting the character rule into platform/audit parameterized by
// length the next time one more appears.
func auditIdentifier(s string, maxLen int) string {
	if s == "" || len(s) > maxLen {
		return redactedIdentifier
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '-', c == '.':
		default:
			return redactedIdentifier
		}
	}
	return s
}
