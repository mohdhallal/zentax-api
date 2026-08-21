package domain

func ErrTaskInstanceNotFound(id TaskInstanceID) string {
	return "task instance not found: " + id
}
