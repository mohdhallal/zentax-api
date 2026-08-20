package middlewares

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
)

type panicError struct {
	value string
	stack string
}

func (e *panicError) Error() string      { return e.value }
func (e *panicError) StackTrace() string { return e.stack }

func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				err := &panicError{
					value: fmt.Sprintf("panic: %v", rec),
					stack: string(debug.Stack()),
				}
				httperr.HandleError(w, r, err)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
