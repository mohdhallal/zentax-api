package apperrors

type ForbiddenError struct {
	Message string
	stack   string
}

func (e *ForbiddenError) Error() string      { return e.Message }
func (e *ForbiddenError) IsForbidden() bool  { return true }
func (e *ForbiddenError) StackTrace() string { return e.stack }

func NewForbidden(message string) *ForbiddenError {
	return &ForbiddenError{Message: message, stack: captureStack()}
}
