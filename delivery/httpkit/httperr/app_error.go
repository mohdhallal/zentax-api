package httperr

import "fmt"

type AppError struct {
	Code    AppErrorCode `json:"code"`
	Message string       `json:"message"`
	Status  int          `json:"-"`
	Details any          `json:"details,omitempty"`
}

func (e *AppError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func New(code AppErrorCode, message string, details ...any) *AppError {
	status := statusMap[code]
	if status == 0 {
		status = 500
	}
	var det any
	if len(details) > 0 {
		det = details[0]
	}
	return &AppError{
		Code:    code,
		Message: message,
		Status:  status,
		Details: det,
	}
}

func (e *AppError) jsonBody() map[string]any {
	obj := map[string]any{
		"code":    e.Code,
		"message": e.Message,
	}
	if e.Details != nil {
		obj["details"] = e.Details
	}
	return obj
}
