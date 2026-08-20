package apperrors

type ValidationError struct {
	Message string
	stack   string
}

func (e *ValidationError) Error() string      { return e.Message }
func (e *ValidationError) IsValidation() bool { return true }
func (e *ValidationError) StackTrace() string { return e.stack }

func NewValidation(message string) *ValidationError {
	return &ValidationError{Message: message, stack: captureStack()}
}
