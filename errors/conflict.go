package apperrors

type ConflictError struct {
	Message string
	stack   string
}

func (e *ConflictError) Error() string      { return e.Message }
func (e *ConflictError) IsConflict() bool   { return true }
func (e *ConflictError) StackTrace() string { return e.stack }

func NewConflict(message string) *ConflictError {
	return &ConflictError{Message: message, stack: captureStack()}
}
