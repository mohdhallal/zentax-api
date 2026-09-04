package domain

func ErrWorkflowTaskNotFound(id WorkflowTaskID) string {
	return "workflow task not found: " + id
}

func ErrWorkflowNotFound() string {
	return "referenced workflow not found in this tenant"
}

func ErrDataTemplateNotFound() string {
	return "data template not found in this tenant"
}
