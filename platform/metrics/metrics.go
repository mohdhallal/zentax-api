package metrics

import (
	"net/http"
	"time"
)

const (
	MetricNameHTTPInflight        = "http_requests_inflight"
	MetricNameHTTPRequestDuration = "http_request_duration_seconds"
	MetricNameDBOperationDuration = "db_operation_duration_seconds"
	MetricNameClientDuration      = "client_request_duration_seconds"
)

const (
	TagMethod     = "method"
	TagRoute      = "route"
	TagStatusCode = "status_code"
	TagOperation  = "operation"
	TagTableName  = "table_name"
	TagClientName = "client_name"
)

type Recorder interface {
	HTTP() HTTPRecorder
	DB() DBRecorder
	Client() ClientRecorder
	Handler() http.Handler
}

type HTTPRecorder interface {
	InFlightInc()
	InFlightDec()
	ObserveDuration(method, route, status string, duration time.Duration)
}

type DBRecorder interface {
	ObserveDuration(operation, tableName string, duration time.Duration)
}

type ClientRecorder interface {
	ObserveDuration(clientName, operation string, duration time.Duration)
}
