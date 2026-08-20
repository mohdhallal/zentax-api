package acceptance

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type TestClient struct {
	externalURL string
	internalURL string
	http        *http.Client
}

func NewTestClient(externalURL, internalURL string) *TestClient {
	return &TestClient{
		externalURL: externalURL,
		internalURL: internalURL,
		http:        &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *TestClient) External() *RequestBuilder {
	return &RequestBuilder{client: c.http, baseURL: c.externalURL}
}

func (c *TestClient) Internal() *RequestBuilder {
	return &RequestBuilder{client: c.http, baseURL: c.internalURL}
}

type RequestBuilder struct {
	client  *http.Client
	baseURL string
	headers http.Header
}

func (b *RequestBuilder) clone() *RequestBuilder {
	h := make(http.Header)
	for k, v := range b.headers {
		h[k] = v
	}
	return &RequestBuilder{client: b.client, baseURL: b.baseURL, headers: h}
}

func (b *RequestBuilder) withHeader(key, val string) *RequestBuilder {
	c := b.clone()
	c.headers.Set(key, val)
	return c
}

// WithExternalAuth sets gateway-injected headers that RequireExternalAuth reads.
func (b *RequestBuilder) WithExternalAuth(accountID int64) *RequestBuilder {
	return b.
		withHeader("X-Account-Id", fmt.Sprintf("%d", accountID)).
		withHeader("X-API-Key-Id", "test-key-id").
		withHeader("X-Customer-Id", "test-customer").
		withHeader("X-Correlation-Id", uuid.NewString())
}

// WithInternalAuth sets Basic Auth credentials for RequireInternalAuth.
func (b *RequestBuilder) WithInternalAuth(key, secret string) *RequestBuilder {
	encoded := base64.StdEncoding.EncodeToString([]byte(key + ":" + secret))
	return b.withHeader("Authorization", "Basic "+encoded)
}

func (b *RequestBuilder) do(t *testing.T, method, path string, body any) *TestResponse {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, b.baseURL+path, bodyReader)
	require.NoError(t, err)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vals := range b.headers {
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}

	resp, err := b.client.Do(req)
	require.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)

	return &TestResponse{Response: resp, body: respBody}
}

func (b *RequestBuilder) GET(t *testing.T, path string) *TestResponse {
	return b.do(t, http.MethodGet, path, nil)
}

func (b *RequestBuilder) POST(t *testing.T, path string, body any) *TestResponse {
	return b.do(t, http.MethodPost, path, body)
}

func (b *RequestBuilder) PUT(t *testing.T, path string, body any) *TestResponse {
	return b.do(t, http.MethodPut, path, body)
}

func (b *RequestBuilder) PATCH(t *testing.T, path string, body any) *TestResponse {
	return b.do(t, http.MethodPatch, path, body)
}

func (b *RequestBuilder) DELETE(t *testing.T, path string) *TestResponse {
	return b.do(t, http.MethodDelete, path, nil)
}
