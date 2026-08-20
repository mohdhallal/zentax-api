package apperrors

type UnauthorizedError struct {
	Message string
	stack   string
}

func (e *UnauthorizedError) Error() string        { return e.Message }
func (e *UnauthorizedError) IsUnauthorized() bool { return true }
func (e *UnauthorizedError) StackTrace() string   { return e.stack }

func NewUnauthorized(message string) *UnauthorizedError {
	return &UnauthorizedError{Message: message, stack: captureStack()}
}
