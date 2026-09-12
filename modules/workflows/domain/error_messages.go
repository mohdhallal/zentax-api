package domain

import "strconv"

func ErrWorkflowNotFound(id WorkflowID) string {
	return "workflow not found: " + id
}

// ErrWorkflowHasApprovedWork is the ADR-0018 refusal: the workflow cannot be
// deleted because approved task instances below it are attested evidence. It
// names what is in the way, how many, and the way out — a workflow can be
// retired by setting its status to `archived`, which keeps the record.
// A count of zero (the database trigger refused a delete the use case did not
// see, e.g. a concurrent approval) drops the number rather than printing "0".
func ErrWorkflowHasApprovedWork(approved int) string {
	what := "approved task instances below it are"
	if approved > 0 {
		what = strconv.Itoa(approved) + " approved task instance(s) below it are"
	}
	return "workflow cannot be deleted: " + what +
		" attested evidence (ADR-0018) — archive the workflow instead (status: archived)"
}

func ErrWorkflowRefNotFound() string {
	return "referenced entity or obligation type not found in this tenant"
}
