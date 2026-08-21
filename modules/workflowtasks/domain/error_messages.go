package domain

func ErrWorkflowTaskNotFound(id WorkflowTaskID) string {
	return "workflow task not found: " + id
}

func ErrWorkflowNotFound() string {
	return "referenced workflow not found in this tenant"
}
