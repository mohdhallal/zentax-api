package apiclient

import (
	"encoding/json"
	"errors"
	"fmt"
)

// APIError is a failure response from the API: the {"status":false,"error":
// {"code","message"}} envelope, plus the request that produced it. Transport
// failures (connection refused, timeout) are NOT APIErrors — they are plain
// wrapped errors, so `IsStatus(err, 409)` cannot mistake a dead server for a
// business conflict.
type APIError struct {
	Status      int    // HTTP status code
	Code        string // envelope error code, e.g. "CONFLICT", "INVALID_BODY"
	Message     string // envelope error message (HTTP status text when absent)
	Method      string // request method
	Path        string // request path (as given to the client, without the base URL)
	RequestID   string // X-Request-Id, when the API returned one
	Details     json.RawMessage
	BodySnippet string // the raw body, truncated — the fallback when the shape is unexpected
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("apiclient: %s %s: %d", e.Method, e.Path, e.Status)
	if e.Code != "" {
		msg += " " + e.Code
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.RequestID != "" {
		msg += " (request " + e.RequestID + ")"
	}
	return msg
}

// AsAPIError extracts the *APIError from err, if there is one.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// IsStatus reports whether err is an API failure with this HTTP status.
func IsStatus(err error, status int) bool {
	apiErr, ok := AsAPIError(err)
	return ok && apiErr.Status == status
}

// IsCode reports whether err is an API failure with this envelope error code.
func IsCode(err error, code string) bool {
	apiErr, ok := AsAPIError(err)
	return ok && apiErr.Code == code
}

// IsNotFound reports a 404.
func IsNotFound(err error) bool { return IsStatus(err, 404) }

// IsConflict reports a 409 — e.g. a slug or code that already exists.
func IsConflict(err error) bool { return IsStatus(err, 409) }

// IsUnauthorized reports a 401 — no session, an expired one, or MFA pending.
func IsUnauthorized(err error) bool { return IsStatus(err, 401) }

// IsForbidden reports a 403 — authenticated, but the grant does not allow it.
func IsForbidden(err error) bool { return IsStatus(err, 403) }
