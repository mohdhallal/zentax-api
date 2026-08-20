package domain

func ErrEntityObligationNotFound(id EntityObligationID) string {
	return "entity obligation not found: " + id
}

func ErrEntityObligationRefNotFound() string {
	return "referenced entity or obligation type not found in this tenant"
}
