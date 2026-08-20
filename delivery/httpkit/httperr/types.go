package httperr

type AppErrorCode string

const (
	ErrValidation   AppErrorCode = "VALIDATION"
	ErrUnauthorized AppErrorCode = "UNAUTHORIZED"
	ErrForbidden    AppErrorCode = "FORBIDDEN"
	ErrNotFound     AppErrorCode = "NOT_FOUND"
	ErrConflict     AppErrorCode = "CONFLICT"
	ErrInternal     AppErrorCode = "INTERNAL"
)

var statusMap = map[AppErrorCode]int{
	ErrValidation:   400,
	ErrUnauthorized: 401,
	ErrForbidden:    403,
	ErrNotFound:     404,
	ErrConflict:     409,
	ErrInternal:     500,
}

type NotFoundErr interface {
	error
	IsNotFound() bool
}

type ConflictErr interface {
	error
	IsConflict() bool
}

type ValidationErr interface {
	error
	IsValidation() bool
}

type ForbiddenErr interface {
	error
	IsForbidden() bool
}

type UnauthorizedErr interface {
	error
	IsUnauthorized() bool
}
