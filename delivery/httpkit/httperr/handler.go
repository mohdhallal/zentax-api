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

// HandleError writes the error envelope to the caller and one line to the log.
//
// ADR-0015: the RESPONSE may carry the message and the details — they are the
// caller's own input coming back — but the LOG may not. A log sits outside the
// erasure boundary, and these two fields are the only ones on this line whose
// content is not server-authored:
//
//   - Message, on a domain error, is err.Error(): "unknown timezone: <what the
//     caller sent>", "unknown task override: <key>". Free text, caller-chosen.
//   - Details is `any` and was handed to the log whole, which is the "never log
//     whole objects blindly" rule the ADR writes down. Today it is the
//     validator's rendered text ("name: is required"); nothing stops the next
//     caller putting a request body in it, and key-based redaction cannot see
//     inside a value.
//
// So the log line carries what identifies the failure and nothing that
// describes its content: method, redacted path, code, the Go type of any
// details, and — for a 5xx only — the error's type and the stack. The
// underlying error's text is deliberately NOT logged even at 5xx: a Postgres
// unique violation carries `Key (email)=(…)` in its Detail. requestId (from the
// context extractor) is what ties this line to the caller's report.
func HandleError(w http.ResponseWriter, r *http.Request, err error) {
	appErr := Classify(err)
	l := logger.Log.WithContext(r.Context())

	fields := []logger.Field{
		logger.String("method", r.Method),
		logger.String("path", logger.RedactPath(r.URL.Path)),
		logger.String("code", string(appErr.Code)),
	}

	if appErr.Details != nil {
		// The shape, never the content. "string" today, a typed struct if one
		// is ever attached — either way it says a detail exists and what kind,
		// and carries nothing of the request.
		fields = append(fields, logger.String("detailsType", fmt.Sprintf("%T", appErr.Details)))
	}

	if appErr.Code == ErrInternal {
		fields = append(fields, logger.String("errorType", fmt.Sprintf("%T", err)))
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
