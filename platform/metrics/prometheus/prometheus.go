package prometheus

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mohamadhallal/zentax-api/platform/metrics"
)

var (
	_ metrics.Recorder       = (*recorder)(nil)
	_ metrics.HTTPRecorder   = (*httpRecorder)(nil)
	_ metrics.DBRecorder     = (*dbRecorder)(nil)
	_ metrics.ClientRecorder = (*clientRecorder)(nil)
)

var unsafeChars = regexp.MustCompile(`[^a-z0-9_]`)

type recorder struct {
	httpRec   *httpRecorder
	dbRec     *dbRecorder
	clientRec *clientRecorder
	handler   http.Handler
}

func New(namespace string) (*recorder, error) {
	ns := safeString(namespace)
	registerer := prometheus.DefaultRegisterer

	httpInflight := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns,
		Name:      metrics.MetricNameHTTPInflight,
		Help:      "Number of HTTP requests currently being processed.",
	})
	if err := registerer.Register(httpInflight); err != nil {
		return nil, fmt.Errorf("prometheus: register %s: %w", metrics.MetricNameHTTPInflight, err)
	}

	httpDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns,
		Name:      metrics.MetricNameHTTPRequestDuration,
		Help:      "Duration of HTTP requests in seconds.",
		Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{metrics.TagMethod, metrics.TagRoute, metrics.TagStatusCode})
	if err := registerer.Register(httpDuration); err != nil {
		return nil, fmt.Errorf("prometheus: register %s: %w", metrics.MetricNameHTTPRequestDuration, err)
	}

	dbDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns,
		Name:      metrics.MetricNameDBOperationDuration,
		Help:      "Duration of database operations in seconds.",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
	}, []string{metrics.TagOperation, metrics.TagTableName})
	if err := registerer.Register(dbDuration); err != nil {
		return nil, fmt.Errorf("prometheus: register %s: %w", metrics.MetricNameDBOperationDuration, err)
	}

	clientDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns,
		Name:      metrics.MetricNameClientDuration,
		Help:      "Duration of external client requests in seconds.",
		Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{metrics.TagClientName, metrics.TagOperation})
	if err := registerer.Register(clientDuration); err != nil {
		return nil, fmt.Errorf("prometheus: register %s: %w", metrics.MetricNameClientDuration, err)
	}

	return &recorder{
		httpRec:   &httpRecorder{inflight: httpInflight, duration: httpDuration},
		dbRec:     &dbRecorder{duration: dbDuration},
		clientRec: &clientRecorder{duration: clientDuration},
		handler:   promhttp.Handler(),
	}, nil
}

func (r *recorder) HTTP() metrics.HTTPRecorder     { return r.httpRec }
func (r *recorder) DB() metrics.DBRecorder         { return r.dbRec }
func (r *recorder) Client() metrics.ClientRecorder { return r.clientRec }
func (r *recorder) Handler() http.Handler          { return r.handler }

type httpRecorder struct {
	inflight prometheus.Gauge
	duration *prometheus.HistogramVec
}

func (h *httpRecorder) InFlightInc() { h.inflight.Inc() }
func (h *httpRecorder) InFlightDec() { h.inflight.Dec() }

func (h *httpRecorder) ObserveDuration(method, route, status string, duration time.Duration) {
	h.duration.WithLabelValues(
		safeString(method),
		safeString(route),
		status,
	).Observe(duration.Seconds())
}

type dbRecorder struct {
	duration *prometheus.HistogramVec
}

func (d *dbRecorder) ObserveDuration(operation, tableName string, duration time.Duration) {
	d.duration.WithLabelValues(
		safeString(operation),
		safeString(tableName),
	).Observe(duration.Seconds())
}

type clientRecorder struct {
	duration *prometheus.HistogramVec
}

func (c *clientRecorder) ObserveDuration(clientName, operation string, duration time.Duration) {
	c.duration.WithLabelValues(
		safeString(clientName),
		safeString(operation),
	).Observe(duration.Seconds())
}

func safeString(s string) string {
	return unsafeChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "_")
}
