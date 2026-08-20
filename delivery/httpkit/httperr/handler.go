package httperr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/logger"
)

func Write(w http.ResponseWriter, err *AppError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": false,
		"error":  err.jsonBody(),
	})
}

type stackTracer interface {
	StackTrace() string
}

func captureStack() string {
	const maxDepth = 32
	pcs := make([]uintptr, maxDepth) //nolint:makezero // runtime.Callers requires pre-allocated buffer
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	var sb strings.Builder
	for {
		frame, more := frames.Next()
		sb.WriteString(frame.Function)
		sb.WriteByte('\n')
		sb.WriteByte('\t')
		sb.WriteString(frame.File)
		sb.WriteByte(':')
		sb.WriteString(strings.TrimRight(strings.Repeat(" ", 0), " "))
		fmt.Fprintf(&sb, "%d\n", frame.Line)
		if !more {
			break
		}
	}
	return sb.String()
}

func HandleError(w http.ResponseWriter, r *http.Request, err error) {
	appErr := Classify(err)
	l := logger.Log.WithContext(r.Context())

	fields := []logger.Field{
		logger.String("method", r.Method),
		logger.String("path", r.URL.Path),
		logger.String("code", string(appErr.Code)),
		logger.String("message", appErr.Message),
	}

	if appErr.Details != nil {
		fields = append(fields, logger.Any("details", appErr.Details))
	}

	if appErr.Code == ErrInternal {
		if st, ok := err.(stackTracer); ok {
			fields = append(fields, logger.String("stack", st.StackTrace()))
		} else {
			fields = append(fields, logger.String("stack", captureStack()))
		}
		l.Error("Internal server error", fields...)
		if config.IsDevelopment() {
			appErr.Details = err.Error()
		}
	} else {
		l.Warn("Request error", fields...)
	}

	Write(w, appErr)
}

func Classify(err error) *AppError {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	var nf NotFoundErr
	if errors.As(err, &nf) && nf.IsNotFound() {
		return New(ErrNotFound, err.Error())
	}
	var cf ConflictErr
	if errors.As(err, &cf) && cf.IsConflict() {
		return New(ErrConflict, err.Error())
	}
	var vl ValidationErr
	if errors.As(err, &vl) && vl.IsValidation() {
		return New(ErrValidation, err.Error())
	}
	var fb ForbiddenErr
	if errors.As(err, &fb) && fb.IsForbidden() {
		return New(ErrForbidden, err.Error())
	}
	var ua UnauthorizedErr
	if errors.As(err, &ua) && ua.IsUnauthorized() {
		return New(ErrUnauthorized, err.Error())
	}
	return New(ErrInternal, "Internal server error")
}

func NotFoundHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		Write(w, New(ErrNotFound, "Endpoint not found"))
	}
}
