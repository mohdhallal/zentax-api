package apperrors

type NotFoundError struct {
	Message string
	stack   string
}

func (e *NotFoundError) Error() string      { return e.Message }
func (e *NotFoundError) IsNotFound() bool   { return true }
func (e *NotFoundError) StackTrace() string { return e.stack }

func NewNotFound(message string) *NotFoundError {
	return &NotFoundError{Message: message, stack: captureStack()}
}
