package mock

import (
	"net/http"
	"time"

	"github.com/mohamadhallal/zentax-api/platform/metrics"
)

var _ metrics.Recorder = (*recorder)(nil)

type recorder struct{}

func New() metrics.Recorder {
	return &recorder{}
}

func (r *recorder) HTTP() metrics.HTTPRecorder     { return &httpRecorder{} }
func (r *recorder) DB() metrics.DBRecorder         { return &dbRecorder{} }
func (r *recorder) Client() metrics.ClientRecorder { return &clientRecorder{} }
func (r *recorder) Handler() http.Handler          { return http.NotFoundHandler() }

type httpRecorder struct{}

func (h *httpRecorder) InFlightInc()                                    {}
func (h *httpRecorder) InFlightDec()                                    {}
func (h *httpRecorder) ObserveDuration(_, _, _ string, _ time.Duration) {}

type dbRecorder struct{}

func (d *dbRecorder) ObserveDuration(_, _ string, _ time.Duration) {}

type clientRecorder struct{}

func (c *clientRecorder) ObserveDuration(_, _ string, _ time.Duration) {}
