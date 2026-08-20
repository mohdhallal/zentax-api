package domain

func ErrObligationTypeNotFound(id ObligationTypeID) string {
	return "obligation type not found: " + id
}

func ErrObligationTypeCodeExists(code string) string {
	return "obligation type code already exists: " + code
}
