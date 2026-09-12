// Package apiclient is a small, dependency-light HTTP client for the ZenTax
// API. Unlike the acceptance suite's client (a separate Go module whose every
// method takes *testing.T and fails the test on error) this one is usable from
// ordinary programs: every method returns an error, and failures carry enough
// context — method, path, status, error code, a body snippet — to be reported
// straight to an operator.
//
// Three rules the API imposes, honoured here:
//
//   - Envelope. Success is {"status":true,"data":…} (plus "pagination" on list
//     routes), failure is {"status":false,"error":{"code","message"}}. Callers
//     see the unwrapped data or a *APIError, never the envelope.
//   - Session cookie. Authentication is the zentax_session cookie (ADR-0011).
//     Cookies are handled by hand — no http.CookieJar — so one process can act
//     as several users at once: Login returns a *Session (a Client bound to
//     that token) and Client.WithSession makes a shallow copy bound to any
//     token. A Client is safe for concurrent use.
//   - Backpressure. The API rate-limits per client address and per principal
//     (delivery/httpkit/middlewares/ratelimit) and answers a shed request 429
//     with Retry-After. A shed request never reaches the handler, so waiting
//     and re-sending it is safe — see roundTrip, the one narrow exception to
//     the client's otherwise strict one-request-per-call rule.
//   - No Origin header. The API's CSRF check is CrossOriginGuard
//     (delivery/httpkit/middlewares/csrf.go): it rejects a mutation whose Origin
//     host differs from the request host. It is wired in the route builder
//     around EVERY state-changing route of the external router — the public
//     /auth mutations, login and logout included, not only the
//     session-authenticated ones — so every POST/PUT/PATCH/DELETE this client
//     makes is subject to it. Non-browser clients that omit Origin are allowed,
//     and this client never sets an Origin header, on any request.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// SessionCookieName is the cookie the API authenticates sessions with.
	SessionCookieName = "zentax_session"

	// DefaultTimeout matches the acceptance client: a shared, fsync-bound
	// compose Postgres can pause for tens of seconds under a parallel suite,
	// and a request must not fail on that.
	DefaultTimeout = 90 * time.Second

	// DefaultUserAgent identifies this client in the API's request log.
	DefaultUserAgent = "zentax-apiclient/1"

	// bodySnippetMax bounds how much of an unexpected response body is kept
	// on an error (enough to diagnose, small enough to log).
	bodySnippetMax = 512

	// DefaultThrottleRetries is how many times a 429 is waited out and the
	// request re-sent. A sweep of the reporting routes (cmd/seed-demo verify
	// makes several hundred calls as ONE principal, as fast as the API will
	// answer) legitimately spends the per-principal budget, and the limiter
	// then hands back a Retry-After of a second or so; without this the tool
	// reports the control firing as if the data were wrong.
	DefaultThrottleRetries = 5

	// ThrottleFallbackWait is used when a 429 carries no usable Retry-After,
	// and ThrottleMaxWait caps what a server can make the client wait — a
	// misconfigured Retry-After of an hour must not hang a deploy check.
	ThrottleFallbackWait = time.Second
	ThrottleMaxWait      = 10 * time.Second
)

// Client talks to one ZenTax API base URL, optionally bound to one session.
// The zero value is not usable — build one with New.
type Client struct {
	baseURL         string
	http            *http.Client
	session         string // zentax_session value; "" = anonymous
	userAgent       string
	throttleRetries int
}

// Option customises a Client at construction.
type Option func(*Client)

// WithHTTPClient supplies the underlying *http.Client (its Jar, if any, is the
// caller's business — this package never relies on one). Replaces any timeout
// set by WithTimeout, so pass it first.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithTimeout overrides DefaultTimeout on the client's own *http.Client.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.http.Timeout = d
		}
	}
}

// WithUserAgent overrides DefaultUserAgent.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// WithThrottleRetries overrides DefaultThrottleRetries. Zero (or negative)
// disables the 429 backoff, so a shed response reaches the caller as a
// *APIError on the first try — what a test asserting the limiter wants.
func WithThrottleRetries(n int) Option {
	return func(c *Client) {
		if n < 0 {
			n = 0
		}
		c.throttleRetries = n
	}
}

// New builds a Client for baseURL (e.g. "http://localhost:3000" — the Go API
// serves no /api prefix; that belongs to the Express proxy).
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:         strings.TrimRight(baseURL, "/"),
		http:            &http.Client{Timeout: DefaultTimeout},
		userAgent:       DefaultUserAgent,
		throttleRetries: DefaultThrottleRetries,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// BaseURL returns the base URL the client was built with (no trailing slash).
func (c *Client) BaseURL() string { return c.baseURL }

// SessionToken returns the session cookie value this client sends ("" when
// anonymous).
func (c *Client) SessionToken() string { return c.session }

// WithSession returns a shallow copy bound to token, sharing the underlying
// *http.Client. The receiver is not modified, so several sessions can be
// derived from one anonymous client and used side by side.
func (c *Client) WithSession(token string) *Client {
	clone := *c
	clone.session = token
	return &clone
}

// GET performs a GET. body is normally nil (it exists so the four verbs share
// one signature); out may be nil to discard the payload.
func (c *Client) GET(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodGet, path, body, out)
}

// POST performs a POST with a JSON body (nil sends no body).
func (c *Client) POST(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPost, path, body, out)
}

// PUT performs a PUT with a JSON body.
func (c *Client) PUT(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPut, path, body, out)
}

// DELETE performs a DELETE (204 responses decode nothing and return nil).
func (c *Client) DELETE(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodDelete, path, body, out)
}

// Do performs any method with an optional JSON body, unwraps the envelope into
// out and returns a *APIError for a failure response.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	_, err := c.doJSON(ctx, method, path, body, out)
	return err
}

// GetPaginated is GET for a list route: it decodes the "data" array into out
// and returns the "pagination" object alongside. Pagination.Present reports
// whether the response actually carried one.
func (c *Client) GetPaginated(ctx context.Context, path string, out any) (Pagination, error) {
	return c.doJSON(ctx, http.MethodGet, path, nil, out)
}

// GetRaw performs a GET and returns the response body verbatim — for routes
// that stream bytes instead of an envelope (document downloads, CSV exports).
// A non-2xx status still becomes a *APIError.
func (c *Client) GetRaw(ctx context.Context, path string) ([]byte, http.Header, error) {
	raw, err := c.roundTrip(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, nil, err
	}
	if raw.statusCode < 200 || raw.statusCode >= 300 {
		return nil, raw.header, newAPIError(http.MethodGet, path, raw)
	}
	return raw.body, raw.header, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) (Pagination, error) {
	raw, err := c.request(ctx, method, path, body)
	if err != nil {
		return Pagination{}, err
	}
	return decodeEnvelope(method, path, raw, out)
}

// request sends one JSON request (body nil = no body) and returns the raw
// exchange, so callers that need response headers — Login and its cookie — can
// have them.
func (c *Client) request(ctx context.Context, method, path string, body any) (*rawResponse, error) {
	var (
		reader      io.Reader
		contentType string
	)
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("apiclient: %s %s: encode body: %w", method, path, err)
		}
		reader = bytes.NewReader(encoded)
		contentType = "application/json"
	}
	return c.roundTrip(ctx, method, path, reader, contentType)
}

// PostMultipart uploads one file (plus text fields) as multipart/form-data —
// the shape POST /workflows/{id}/documents and POST /documents/{id}/versions
// expect (ADR-0022). fileField defaults to "file", the name the API reads. The
// part's Content-Type is derived from fileName (see ContentTypeFor): the server
// stores it as the document's MIME type.
func (c *Client) PostMultipart(
	ctx context.Context,
	path string,
	fields map[string]string,
	fileField, fileName string,
	content []byte,
	out any,
) error {
	if fileField == "" {
		fileField = "file"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return fmt.Errorf("apiclient: POST %s: write field %q: %w", path, k, err)
		}
	}
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition",
		`form-data; name="`+escapeQuotes(fileField)+`"; filename="`+escapeQuotes(fileName)+`"`)
	hdr.Set("Content-Type", ContentTypeFor(fileName, content))
	part, err := mw.CreatePart(hdr)
	if err != nil {
		return fmt.Errorf("apiclient: POST %s: create file part: %w", path, err)
	}
	if _, err := part.Write(content); err != nil {
		return fmt.Errorf("apiclient: POST %s: write file part: %w", path, err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("apiclient: POST %s: close multipart body: %w", path, err)
	}

	raw, err := c.roundTrip(ctx, http.MethodPost, path, &buf, mw.FormDataContentType())
	if err != nil {
		return err
	}
	_, err = decodeEnvelope(http.MethodPost, path, raw, out)
	return err
}

// ContentTypeFor picks the MIME type declared for an uploaded file part. The
// table is explicit (not mime.TypeByExtension) so the result does not depend on
// the host's /etc/mime.types — a scratch container and a laptop must agree.
func ContentTypeFor(fileName string, content []byte) string {
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".zip":
		return "application/zip"
	}
	if len(content) > 0 {
		return http.DetectContentType(content)
	}
	return "application/octet-stream"
}

// rawResponse is one completed HTTP exchange, body already drained.
type rawResponse struct {
	statusCode int
	header     http.Header
	body       []byte
}

func (r *rawResponse) requestID() string { return r.header.Get("X-Request-Id") }

// roundTrip sends the request, waiting out a 429 and re-sending it up to
// throttleRetries times. It returns an error only for transport-level
// failures; any HTTP status reaches the caller in rawResponse.
//
// This is the client's only retry besides WaitReady, and it is narrow on
// purpose. The rule it bends — one request per call, so a repeated POST cannot
// duplicate a row and a repeated GET cannot paper over a flapping API — exists
// because a retried request may ALREADY have been executed. A 429 from
// ratelimit is the one status that proves the opposite: the limiter sheds
// before the handler, before the transaction, before the body is even read, so
// nothing happened and re-sending is the behaviour the Retry-After header asks
// for. Any other status, 5xx included, still reaches the caller untouched.
//
// A body that cannot be rewound (the multipart buffer) is never re-sent; that
// 429 surfaces to the caller instead of being silently truncated.
func (c *Client) roundTrip(
	ctx context.Context, method, path string, body io.Reader, contentType string,
) (*rawResponse, error) {
	rewind, rewindable := bodyRewinder(body)
	for attempt := 0; ; attempt++ {
		raw, err := c.sendOnce(ctx, method, path, body, contentType)
		if err != nil {
			return nil, err
		}
		if raw.statusCode != http.StatusTooManyRequests || attempt >= c.throttleRetries || !rewindable {
			return raw, nil
		}
		select {
		case <-ctx.Done():
			// Hand back the 429 itself: the caller's error then names the real
			// reason the call failed rather than the deadline it ran into.
			return raw, nil
		case <-time.After(retryAfter(raw.header)):
		}
		if err := rewind(); err != nil {
			return raw, nil
		}
	}
}

// retryAfter reads the Retry-After of a shed response, clamped into
// [ThrottleFallbackWait, ThrottleMaxWait]. Both RFC 9110 forms are accepted:
// delta-seconds (what ratelimit.Shed sends) and an HTTP-date.
func retryAfter(header http.Header) time.Duration {
	raw := strings.TrimSpace(header.Get("Retry-After"))
	wait := ThrottleFallbackWait
	if secs, err := strconv.Atoi(raw); err == nil {
		wait = time.Duration(secs) * time.Second
	} else if when, err := http.ParseTime(raw); err == nil {
		wait = time.Until(when)
	}
	if wait < ThrottleFallbackWait {
		return ThrottleFallbackWait
	}
	if wait > ThrottleMaxWait {
		return ThrottleMaxWait
	}
	return wait
}

// bodyRewinder reports whether body can be sent a second time, and returns the
// func that resets it. A nil body needs nothing; *bytes.Reader (every JSON
// body) seeks; *bytes.Buffer (the multipart upload) is consumed by the first
// send and cannot.
func bodyRewinder(body io.Reader) (func() error, bool) {
	if body == nil {
		return func() error { return nil }, true
	}
	if seeker, ok := body.(io.Seeker); ok {
		return func() error {
			_, err := seeker.Seek(0, io.SeekStart)
			return err
		}, true
	}
	return nil, false
}

// sendOnce performs exactly one HTTP exchange.
func (c *Client) sendOnce(
	ctx context.Context, method, path string, body io.Reader, contentType string,
) (*rawResponse, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), body)
	if err != nil {
		return nil, fmt.Errorf("apiclient: %s %s: build request: %w", method, path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.session != "" {
		// Set by hand rather than through a cookie jar: one process acts as
		// several users, and a jar would share cookies between them.
		req.Header.Set("Cookie", SessionCookieName+"="+c.session)
	}
	// Deliberately no Origin header: the CSRF check treats an absent Origin as
	// a non-browser client and allows the mutation.

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apiclient: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("apiclient: %s %s: read response: %w", method, path, err)
	}
	return &rawResponse{statusCode: resp.StatusCode, header: resp.Header, body: data}, nil
}

func (c *Client) url(path string) string {
	if path == "" {
		return c.baseURL
	}
	if strings.HasPrefix(path, "/") {
		return c.baseURL + path
	}
	return c.baseURL + "/" + path
}

func escapeQuotes(s string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(s)
}
