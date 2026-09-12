package domain

import "strconv"

func ErrWorkflowTaskNotFound(id WorkflowTaskID) string {
	return "workflow task not found: " + id
}

// ErrWorkflowTaskHasApprovedWork is the ADR-0018 refusal: the template step
// cannot be deleted because approved task instances were generated from it, and
// those are attested evidence. Workflow tasks carry no status column, so there
// is no archive to offer here — the workflow that owns the step is what gets
// archived. A count of zero (the database trigger refused a delete the use case
// did not see) drops the number rather than printing "0".
func ErrWorkflowTaskHasApprovedWork(approved int) string {
	what := "approved task instances were generated from it and are"
	if approved > 0 {
		what = strconv.Itoa(approved) + " approved task instance(s) were generated from it and are"
	}
	return "workflow task cannot be deleted: " + what +
		" attested evidence (ADR-0018) — archive the workflow instead of removing the step"
}

func ErrWorkflowNotFound() string {
	return "referenced workflow not found in this tenant"
}

func ErrDataTemplateNotFound() string {
	return "data template not found in this tenant"
}
