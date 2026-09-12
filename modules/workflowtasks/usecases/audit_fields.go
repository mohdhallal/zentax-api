package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// auditValues is the ADR-0008 whitelist for a workflow task template: the
// fields that decide who must do a step, by when, and whether anyone has to
// sign it off. It feeds audit.Changes, which records the before and after of
// whatever actually moved.
//
// Recorded verbatim (enums, numbers, ids — no personal data, no free text):
//
//   - approvalRequired: the segregation-of-duties switch (ADR-0012). Turning it
//     off means the step's instances need no second pair of eyes, which is the
//     single most control-relevant edit possible on a template.
//   - dueDateReference / dueDateOffsetValue / dueDateOffsetUnit /
//     dueDateOffsetDirection: the offset that dates every instance of the step.
//   - taskType: preparation / review / filing / payment — what kind of work it
//     is, and what the generator defaults from.
//   - orderIndex: the position in a sequential workflow, i.e. what must happen
//     before it.
//   - dataTemplateId: which typed tax-data schema the step collects (ADR-0001).
//   - workflowId: the owning workflow (fixed at creation; recorded so a created
//     template is attributable without a second query).
//   - requiredDocuments: as a shape only — see auditDocumentRequirements.
//
// Redacted — the change is dated, the value withheld: name, description and
// roleLabel. All three are free text a user typed, and a role label can name a
// person ("Maria's review"), which the envelope must never carry.
//
// Left out by design: the actor / timestamp columns (the envelope carries
// those) and the id (resource_id).
func auditValues(wt *domain.WorkflowTask) audit.Values {
	if wt == nil {
		return nil
	}
	return audit.Values{
		"name":                   audit.Redact(wt.Name),
		"description":            audit.Redact(wt.Description),
		"roleLabel":              audit.Redact(wt.RoleLabel),
		"workflowId":             wt.WorkflowID,
		"taskType":               wt.TaskType,
		"approvalRequired":       wt.ApprovalRequired,
		"dueDateReference":       wt.DueDateReference,
		"dueDateOffsetValue":     wt.DueDateOffsetValue,
		"dueDateOffsetUnit":      wt.DueDateOffsetUnit,
		"dueDateOffsetDirection": wt.DueDateOffsetDirection,
		"orderIndex":             wt.OrderIndex,
		"dataTemplateId":         wt.DataTemplateID,
		"requiredDocuments":      auditDocumentRequirements(wt.RequiredDocuments),
	}
}

// auditDocumentRequirements projects the required-document list onto its shape
// — how many documents the step asks for and how many of them are mandatory.
// The names and descriptions are free text and stay out; the consequence is
// deliberate and worth stating: re-wording a requirement leaves no trace here,
// while dropping one, adding one, or making one optional does.
func auditDocumentRequirements(docs domain.DocumentRequirements) map[string]any {
	if len(docs) == 0 {
		return nil
	}
	mandatory := 0
	for _, d := range docs {
		if d.Required {
			mandatory++
		}
	}
	return map[string]any{"count": len(docs), "mandatory": mandatory}
}
