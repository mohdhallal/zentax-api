package types

import "time"

type Pagination struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
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

func (r *HttpResponse) WithPagination(pagination Pagination) *HttpResponse {
	r.Pagination = &pagination
	return r
}
