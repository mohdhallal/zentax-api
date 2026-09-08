package types

import (
	"reflect"
	"time"
)

// Pagination is the block a paginated envelope carries next to `data`. Total
// is the exact count of rows matching the request's filters; HasMore is
// derived from it (`offset + len(data) < total`) so a client never has to
// compute it from a page it may have received short.
type Pagination struct {
	Total   int  `json:"total"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"hasMore"`
}

type HealthCheckResponse struct {
	Status    int        `json:"status"`
	Mode      ServerMode `json:"mode"`
	Timestamp time.Time  `json:"timestamp"`
}

type HttpResponse struct {
	Status     int
	Headers    map[string]string
	Data       any
	Pagination *Pagination
}

// WithPagination attaches the pagination block, deriving HasMore from the
// page already set as Data: another page exists when the rows before and on
// this page do not reach the total. A caller-supplied HasMore is overwritten.
func (r *HttpResponse) WithPagination(pagination Pagination) *HttpResponse {
	pagination.HasMore = pagination.Offset+dataLen(r.Data) < pagination.Total
	r.Pagination = &pagination
	return r
}

// dataLen is the number of rows on the page: the length of a slice/array
// payload, zero for anything else (nil included).
func dataLen(data any) int {
	if data == nil {
		return 0
	}
	v := reflect.ValueOf(data)
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		return v.Len()
	default:
		return 0
	}
}
