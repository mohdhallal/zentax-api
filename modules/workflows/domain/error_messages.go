package domain

func ErrWorkflowNotFound(id WorkflowID) string {
	return "workflow not found: " + id
}

func ErrWorkflowRefNotFound() string {
	return "referenced entity or obligation type not found in this tenant"
}
