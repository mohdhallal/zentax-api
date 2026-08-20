package domain

func ErrEntityNotFound(id EntityID) string {
	return "entity not found: " + id
}
