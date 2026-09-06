package apiclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// Pagination is the "pagination" object list routes return next to "data".
// Present distinguishes "the route returned {total:0,…}" from "the route
// returned no pagination at all" — both are all-zero otherwise.
type Pagination struct {
	Total   int  `json:"total"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	Present bool `json:"-"`
}

// HasMore reports whether another page exists after this one. It is meaningful
// only for a Present pagination whose Limit is non-zero.
func (p Pagination) HasMore() bool {
	return p.Present && p.Limit > 0 && p.Offset+p.Limit < p.Total
}

// envelope is the response body every JSON route returns.
type envelope struct {
	Status     *bool           `json:"status"`
	Data       json.RawMessage `json:"data"`
	Error      *errorBody      `json:"error"`
	Pagination *Pagination     `json:"pagination"`
}

type errorBody struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
}

// decodeEnvelope unwraps one response: it decodes "data" into out on success
// and returns a *APIError otherwise. Numbers inside an `any` target decode as
// json.Number so large money values survive a round trip exactly.
func decodeEnvelope(method, path string, raw *rawResponse, out any) (Pagination, error) {
	ok := raw.statusCode >= 200 && raw.statusCode < 300

	// 204/205 and any other empty body: nothing to unwrap.
	if len(bytes.TrimSpace(raw.body)) == 0 {
		if ok {
			return Pagination{}, nil
		}
		return Pagination{}, newAPIError(method, path, raw)
	}

	var env envelope
	if err := json.Unmarshal(raw.body, &env); err != nil {
		if !ok {
			// An error response that is not the envelope (a proxy's HTML 502,
			// a panic page): still report it as an API error.
			return Pagination{}, newAPIError(method, path, raw)
		}
		return Pagination{}, fmt.Errorf(
			"apiclient: %s %s: status %d: response is not JSON: %s",
			method, path, raw.statusCode, snippet(raw.body))
	}

	if !ok || (env.Status != nil && !*env.Status) {
		return Pagination{}, newAPIError(method, path, raw)
	}
	if env.Status == nil {
		return Pagination{}, fmt.Errorf(
			"apiclient: %s %s: status %d: response has no \"status\" field: %s",
			method, path, raw.statusCode, snippet(raw.body))
	}

	pagination := Pagination{}
	if env.Pagination != nil {
		pagination = *env.Pagination
		pagination.Present = true
	}

	if out == nil || len(env.Data) == 0 {
		return pagination, nil
	}
	dec := json.NewDecoder(bytes.NewReader(env.Data))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return pagination, fmt.Errorf(
			"apiclient: %s %s: decode data: %w (data: %s)", method, path, err, snippet(env.Data))
	}
	return pagination, nil
}

// newAPIError builds the typed error for a failure response, parsing the error
// envelope when there is one and falling back to the raw body otherwise.
func newAPIError(method, path string, raw *rawResponse) *APIError {
	apiErr := &APIError{
		Method:      method,
		Path:        path,
		Status:      raw.statusCode,
		RequestID:   raw.requestID(),
		BodySnippet: snippet(raw.body),
	}
	var env envelope
	if err := json.Unmarshal(raw.body, &env); err == nil && env.Error != nil {
		apiErr.Code = env.Error.Code
		apiErr.Message = env.Error.Message
		apiErr.Details = env.Error.Details
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(raw.statusCode)
	}
	return apiErr
}

func snippet(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > bodySnippetMax {
		return string(trimmed[:bodySnippetMax]) + "…"
	}
	return string(trimmed)
}
