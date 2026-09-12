package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The API sheds a request over its rate budget with 429 + Retry-After, having
// run no handler at all. These pin the client's response to that: wait, re-send
// the identical request, and never let a shed request read as a data defect —
// which is what cmd/seed-demo verify was reporting before it backed off.

// throttleStub replies with the given statuses in order (the last one repeats),
// counting requests and echoing what it received.
func throttleStub(t *testing.T, retryAfter string, statuses ...int) (*Client, *[]recordedRequest, func() int) {
	t.Helper()
	var (
		mu    sync.Mutex
		seen  []recordedRequest
		calls int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(body)
		}
		mu.Lock()
		idx := calls
		calls++
		seen = append(seen, recordedRequest{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
			Header: r.Header.Clone(), Body: body,
		})
		mu.Unlock()

		status := statuses[len(statuses)-1]
		if idx < len(statuses) {
			status = statuses[idx]
		}
		w.Header().Set("Content-Type", "application/json")
		if status == http.StatusTooManyRequests && retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"status":true,"data":{"name":"served"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":false,"error":{"code":"TOO_MANY_REQUESTS","message":"Too many requests"}}`))
	}))
	t.Cleanup(srv.Close)

	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
	return New(srv.URL), &seen, count
}

func TestThrottle_ShedRequestIsWaitedOutAndReSentIdentically(t *testing.T) {
	c, seen, calls := throttleStub(t, "1", http.StatusTooManyRequests, http.StatusOK)

	var out struct{ Name string }
	started := time.Now()
	require.NoError(t, c.POST(context.Background(), "/entities", map[string]any{"name": "Acme"}, &out))

	assert.Equal(t, "served", out.Name, "the retry's payload must reach the caller")
	assert.Equal(t, 2, calls())
	assert.GreaterOrEqual(t, time.Since(started), ThrottleFallbackWait,
		"the client must actually wait out Retry-After, not spin")

	// The re-sent request is byte-identical — the body was rewound, not lost.
	require.Len(t, *seen, 2)
	assert.Equal(t, (*seen)[0].Body, (*seen)[1].Body)
	assert.NotEmpty(t, (*seen)[1].Body, "a re-sent POST must carry its body again")
	assert.Equal(t, (*seen)[0].Method, (*seen)[1].Method)
	assert.Equal(t, (*seen)[0].Path, (*seen)[1].Path)
}

func TestThrottle_ExhaustedRetriesSurfaceThe429(t *testing.T) {
	c, _, calls := throttleStub(t, "1", http.StatusTooManyRequests)
	c = New(c.BaseURL(), WithThrottleRetries(1))

	err := c.GET(context.Background(), "/reports/task-summary", nil, nil)

	require.Error(t, err, "a budget that never refills must not be retried forever")
	assert.True(t, IsStatus(err, http.StatusTooManyRequests), "got %v", err)
	assert.Equal(t, 2, calls(), "one original plus one retry")
}

func TestThrottle_RetriesDisabledSurfacesTheFirst429(t *testing.T) {
	c, _, calls := throttleStub(t, "1", http.StatusTooManyRequests)
	c = New(c.BaseURL(), WithThrottleRetries(0))

	err := c.GET(context.Background(), "/reports/task-summary", nil, nil)

	require.Error(t, err)
	assert.Equal(t, 1, calls(), "a test asserting the limiter must see the shed response")
}

func TestThrottle_OtherStatusesAreNeverRetried(t *testing.T) {
	// 503 may or may not have run the handler, so the client must not repeat
	// it — only a 429 proves nothing happened.
	c, _, calls := throttleStub(t, "", http.StatusServiceUnavailable, http.StatusOK)

	err := c.POST(context.Background(), "/entities", map[string]any{"name": "Acme"}, nil)

	require.Error(t, err)
	assert.Equal(t, 1, calls())
}

func TestThrottle_MultipartUploadIsNotReSent(t *testing.T) {
	// The multipart body is a *bytes.Buffer the first send drains; re-sending
	// it would upload a truncated document, so the 429 surfaces instead.
	c, _, calls := throttleStub(t, "1", http.StatusTooManyRequests, http.StatusOK)

	err := c.PostMultipart(context.Background(), "/workflows/w1/documents",
		map[string]string{"documentType": "return"}, "file", "vat.pdf", []byte("%PDF-1.4"), nil)

	require.Error(t, err)
	assert.True(t, IsStatus(err, http.StatusTooManyRequests), "got %v", err)
	assert.Equal(t, 1, calls())
}

func TestThrottle_ContextCancellationDuringTheWaitReportsTheShed(t *testing.T) {
	c, _, calls := throttleStub(t, "3", http.StatusTooManyRequests)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.GET(ctx, "/reports/task-summary", nil, nil)

	require.Error(t, err)
	assert.True(t, IsStatus(err, http.StatusTooManyRequests),
		"the caller should learn it was shed, not that it timed out: %v", err)
	assert.Equal(t, 1, calls())
}

func TestRetryAfter_ParsesBothFormsAndClamps(t *testing.T) {
	httpDate := time.Now().Add(4 * time.Second).UTC().Format(http.TimeFormat)

	for _, tc := range []struct {
		name   string
		header string
		want   time.Duration
		approx bool
	}{
		{name: "delta-seconds", header: "2", want: 2 * time.Second},
		{name: "absent falls back", header: "", want: ThrottleFallbackWait},
		{name: "unparseable falls back", header: "soon", want: ThrottleFallbackWait},
		{name: "zero is floored", header: "0", want: ThrottleFallbackWait},
		{name: "negative is floored", header: "-5", want: ThrottleFallbackWait},
		{name: "absurd value is capped", header: "3600", want: ThrottleMaxWait},
		{name: "http-date", header: httpDate, want: 4 * time.Second, approx: true},
		{name: "past http-date is floored", header: "Mon, 02 Jan 2006 15:04:05 GMT", want: ThrottleFallbackWait},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.header != "" {
				h.Set("Retry-After", tc.header)
			}
			got := retryAfter(h)
			if tc.approx {
				assert.InDelta(t, tc.want.Seconds(), got.Seconds(), 1.5)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRetryAfter_WhitespaceIsTolerated(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "  2  ")
	assert.Equal(t, 2*time.Second, retryAfter(h))
}

func TestBodyRewinder(t *testing.T) {
	rewind, ok := bodyRewinder(nil)
	require.True(t, ok, "no body is always re-sendable")
	require.NoError(t, rewind())

	reader := strings.NewReader("payload")
	rewind, ok = bodyRewinder(reader)
	require.True(t, ok, "a seekable body is re-sendable")
	_, _ = reader.Read(make([]byte, 4))
	require.NoError(t, rewind())
	rest, err := readAllString(reader)
	require.NoError(t, err)
	assert.Equal(t, "payload", rest, "rewind must restore the whole body")

	_, ok = bodyRewinder(&onePassReader{})
	assert.False(t, ok, "a one-pass body must not be re-sent")
}

type onePassReader struct{}

func (*onePassReader) Read([]byte) (int, error) { return 0, nil }

func readAllString(r *strings.Reader) (string, error) {
	buf := make([]byte, r.Len())
	n, err := r.Read(buf)
	return string(buf[:n]), err
}
