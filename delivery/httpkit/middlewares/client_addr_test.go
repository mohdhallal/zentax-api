package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/clientaddr"
	"github.com/mohamadhallal/zentax-api/platform/securityevent"
)

// bound runs one request through the middleware and returns what reached the
// security stream's context — the address and its provenance, which is what
// ends up in security_events.client_ip / client_ip_source.
func bound(t *testing.T, resolver *clientaddr.Resolver, remoteAddr string, headers map[string]string) (string, string) {
	t.Helper()

	var addr, source string
	handler := ClientAddressMiddleware(resolver)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		addr = securityevent.ClientAddr(r.Context())
		source = securityevent.ClientAddrSource(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPost, "/auth/login", http.NoBody)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return addr, source
}

func clientResolver(t *testing.T, edgeHeader string) *clientaddr.Resolver {
	t.Helper()
	r, err := clientaddr.NewResolver([]string{"10.20.0.0/16"}, "", edgeHeader)
	require.NoError(t, err)
	return r
}

// THE FOUR TOPOLOGIES, as they reach the STORE. The resolver's own tests prove
// the resolution; this proves the middleware binds the provenance with the
// address, because the two are worth different amounts and look identical once
// written to an append-only table.
func TestClientAddressMiddleware_BindsTheAddressAndHowItWasArrivedAt(t *testing.T) {
	plain := clientResolver(t, "")
	edge := clientResolver(t, clientaddr.CloudFrontViewerAddressHeader)

	// 1. Direct: the socket peer, and nothing the caller wrote.
	addr, source := bound(t, plain, "203.0.113.9:51000",
		map[string]string{"X-Forwarded-For": "192.0.2.66"})
	assert.Equal(t, "203.0.113.9", addr)
	assert.Equal(t, securityevent.AddrFromPeer, source)

	// 2. Load balancer only: the chain, walked from the right.
	addr, source = bound(t, plain, "10.20.3.44:44321",
		map[string]string{"X-Forwarded-For": "192.0.2.66, 203.0.113.9, 10.20.1.7"})
	assert.Equal(t, "203.0.113.9", addr)
	assert.Equal(t, securityevent.AddrFromForwarded, source)

	// 3. Through a distribution: the viewer it states, not the edge server the
	// chain ends at.
	addr, source = bound(t, edge, "10.20.3.44:44321", map[string]string{
		"X-Forwarded-For":                        "192.0.2.66, 203.0.113.9, 198.51.100.9, 10.20.1.7",
		clientaddr.CloudFrontViewerAddressHeader: "203.0.113.9:51000",
	})
	assert.Equal(t, "203.0.113.9", addr)
	assert.NotEqual(t, "198.51.100.9", addr, "the CDN edge is not the caller")
	assert.Equal(t, securityevent.AddrFromForwarded, source)

	// 4. A forged viewer address from something that did not come through the
	// distribution — from outside our tier it is not read at all, and from
	// inside it its ABSENCE marks the row as our own hop rather than a caller.
	addr, source = bound(t, edge, "192.0.2.66:51000", map[string]string{
		clientaddr.CloudFrontViewerAddressHeader: "203.0.113.9:443",
	})
	assert.Equal(t, "192.0.2.66", addr)
	assert.Equal(t, securityevent.AddrFromPeer, source)

	addr, source = bound(t, edge, "10.20.3.44:44321",
		map[string]string{"X-Forwarded-For": "192.0.2.66, 203.0.113.9"})
	assert.Equal(t, "10.20.3.44", addr)
	assert.Equal(t, securityevent.AddrFromProxy, source,
		"the fallback has to be readable in the row, not only in the log")
}

// Nothing resolvable binds nothing: "unknown" is a real rate-limit key but it
// is not an address, and a column that holds addresses must not be handed one.
func TestClientAddressMiddleware_UnresolvableRequestBindsNothing(t *testing.T) {
	addr, source := bound(t, clientResolver(t, clientaddr.CloudFrontViewerAddressHeader), "@internal", nil)

	assert.Equal(t, "", addr)
	assert.Equal(t, "", source)
}

func TestClientAddressMiddleware_NilResolverBindsNothing(t *testing.T) {
	addr, source := bound(t, nil, "203.0.113.9:51000", nil)

	assert.Equal(t, "", addr)
	assert.Equal(t, "", source)
}
