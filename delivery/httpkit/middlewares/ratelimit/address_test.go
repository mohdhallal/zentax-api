package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loopbackAndVPC is the shape of a real deployment's trust: the load balancer
// (or the compose reverse proxy) sits on a private address, nothing else does.
var loopbackAndVPC = []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8"}

func resolver(t *testing.T, trusted []string) *AddressResolver {
	t.Helper()
	r, err := NewAddressResolver(trusted, "")
	require.NoError(t, err)
	return r
}

func request(remoteAddr string, forwarded ...string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/auth/login", http.NoBody)
	r.RemoteAddr = remoteAddr
	for _, value := range forwarded {
		r.Header.Add(DefaultForwardedHeader, value)
	}
	return r
}

// THE ONE THAT MATTERS. An attacker on the internet writes a forged
// X-Forwarded-For; the load balancer appends the address it actually saw. Read
// the chain left to right — the common implementation — and every request mints
// a brand-new budget, which is not a weaker limiter but no limiter at all.
func TestClientAddress_ForgedForwardedHeader_CannotEvadeTheLimit(t *testing.T) {
	t.Parallel()

	res := resolver(t, loopbackAndVPC)

	first := res.ClientAddress(request("10.0.1.5:44321", "203.0.113.9, 198.51.100.77"))
	second := res.ClientAddress(request("10.0.1.5:44321", "8.8.8.8, 198.51.100.77"))
	third := res.ClientAddress(request("10.0.1.5:44321", "1.1.1.1, 2.2.2.2, 198.51.100.77"))

	assert.Equal(t, "198.51.100.77", first, "the address the proxy appended is the client")
	assert.Equal(t, first, second, "changing the forged prefix must not change the key")
	assert.Equal(t, first, third, "nor must lengthening it")
}

// The other total failure: believing the header from an untrusted peer. A caller
// reaching the API directly gets its socket address, whatever it claims.
func TestClientAddress_UntrustedPeer_HeaderIgnored(t *testing.T) {
	t.Parallel()

	res := resolver(t, loopbackAndVPC)

	got := res.ClientAddress(request("203.0.113.9:51000", "10.0.0.1"))

	assert.Equal(t, "203.0.113.9", got)
}

func TestClientAddress_NoTrustedProxies_AlwaysTheSocket(t *testing.T) {
	t.Parallel()

	res := resolver(t, nil)

	got := res.ClientAddress(request("10.0.1.5:44321", "198.51.100.77"))

	assert.Equal(t, "10.0.1.5", got, "an empty trust list must key on the socket address")
}

func TestClientAddress_TrustedPeerNoHeader_UsesTheSocket(t *testing.T) {
	t.Parallel()

	res := resolver(t, loopbackAndVPC)

	assert.Equal(t, "10.0.1.5", res.ClientAddress(request("10.0.1.5:44321")))
}

// Two proxies in front (an ALB and an in-cluster ingress): every hop of our own
// infrastructure is skipped and the first foreign address wins.
func TestClientAddress_ChainOfTrustedHops_SkippedRightToLeft(t *testing.T) {
	t.Parallel()

	res := resolver(t, loopbackAndVPC)

	got := res.ClientAddress(request("10.0.9.9:1234", "198.51.100.77, 10.0.1.5, 10.0.2.6"))

	assert.Equal(t, "198.51.100.77", got)
}

// Repeated header lines are one chain, in order (RFC 9110). Splitting only the
// last line would read the wrong end of it.
func TestClientAddress_RepeatedHeaderLines_ConcatenatedInOrder(t *testing.T) {
	t.Parallel()

	res := resolver(t, loopbackAndVPC)

	got := res.ClientAddress(request("10.0.9.9:1234", "203.0.113.9", "198.51.100.77, 10.0.1.5"))

	assert.Equal(t, "198.51.100.77", got)
}

// Every hop was our own: there is no client in the chain, so the peer is the
// best available answer. (This is the shape a health check from the load
// balancer itself arrives in.)
func TestClientAddress_AllHopsTrusted_FallsBackToPeer(t *testing.T) {
	t.Parallel()

	res := resolver(t, loopbackAndVPC)

	assert.Equal(t, "10.0.9.9", res.ClientAddress(request("10.0.9.9:1234", "10.0.1.5, 10.0.2.6")))
}

// Garbage nearest the socket stops the walk rather than being skipped past: we
// can no longer attribute, so we fall back to an address we know is real.
// (Unreachable from a forged header behind an appending proxy — the real
// address is always to the right of whatever the client wrote — but the parser
// must not be the thing that assumes that.)
func TestClientAddress_UnparseableNearestHop_FallsBackToPeer(t *testing.T) {
	t.Parallel()

	res := resolver(t, loopbackAndVPC)

	assert.Equal(t, "10.0.9.9", res.ClientAddress(request("10.0.9.9:1234", "198.51.100.77, not-an-address")))
}

func TestClientAddress_AddressForms(t *testing.T) {
	t.Parallel()

	res := resolver(t, loopbackAndVPC)

	cases := []struct {
		name      string
		remote    string
		forwarded string
		want      string
	}{
		{"bare ipv4 entry", "10.0.1.5:44321", "198.51.100.77", "198.51.100.77"},
		{"entry with a port", "10.0.1.5:44321", "198.51.100.77:9999", "198.51.100.77"},
		{"bare ipv6 entry", "10.0.1.5:44321", "2001:db8::1", "2001:db8::1"},
		{"bracketed ipv6 with a port", "10.0.1.5:44321", "[2001:db8::1]:9999", "2001:db8::1"},
		{"padded entries", "10.0.1.5:44321", "  198.51.100.77  ", "198.51.100.77"},
		{"ipv6 socket peer", "[::1]:44321", "198.51.100.77", "198.51.100.77"},
		// An IPv4-mapped IPv6 address must key the same as its IPv4 form, or
		// one client gets two budgets by switching representation.
		{"ipv4-mapped entry", "10.0.1.5:44321", "::ffff:198.51.100.77", "198.51.100.77"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, res.ClientAddress(request(tc.remote, tc.forwarded)))
		})
	}
}

func TestClientAddress_UnusableSocketAddress_IsItsOwnKey(t *testing.T) {
	t.Parallel()

	res := resolver(t, loopbackAndVPC)

	assert.Equal(t, UnknownAddress, res.ClientAddress(request("", "198.51.100.77")))
	assert.Equal(t, UnknownAddress, res.ClientAddress(request("@internal", "198.51.100.77")))
}

func TestClientAddress_NilReceiverOrRequest(t *testing.T) {
	t.Parallel()

	var nilResolver *AddressResolver
	assert.Equal(t, UnknownAddress, nilResolver.ClientAddress(request("10.0.1.5:1")))
	assert.Equal(t, UnknownAddress, resolver(t, nil).ClientAddress(nil))
}

func TestNewAddressResolver_TrustedProxyForms(t *testing.T) {
	t.Parallel()

	res, err := NewAddressResolver([]string{" 10.0.0.0/8 ", "127.0.0.1", ""}, "")
	require.NoError(t, err)

	// A bare address is a single-host prefix: its neighbour is not trusted.
	assert.Equal(t, "198.51.100.77", res.ClientAddress(request("127.0.0.1:1", "198.51.100.77")))
	assert.Equal(t, "127.0.0.2", res.ClientAddress(request("127.0.0.2:1", "198.51.100.77")))
}

func TestNewAddressResolver_RejectsNonsense(t *testing.T) {
	t.Parallel()

	_, err := NewAddressResolver([]string{"10.0.0.0/99"}, "")
	require.Error(t, err)

	_, err = NewAddressResolver([]string{"not-an-address"}, "")
	require.Error(t, err)
}

func TestNewAddressResolver_CustomHeader(t *testing.T) {
	t.Parallel()

	res, err := NewAddressResolver(loopbackAndVPC, "X-Real-IP")
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = "10.0.1.5:44321"
	r.Header.Set("X-Real-IP", "198.51.100.77")
	r.Header.Set(DefaultForwardedHeader, "203.0.113.9")

	assert.Equal(t, "198.51.100.77", res.ClientAddress(r),
		"only the configured header is read")
}
