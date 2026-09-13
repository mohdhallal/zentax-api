package clientaddr

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loopbackAndVPC is the shape of a deployment whose trusted set really is only
// the hops in front of the api — the load balancer and the web tier sit on
// these private addresses, and (this is the part the deployment has to make
// true, see infra/lib/api-stack.ts) nothing else does. Where that is NOT true,
// anything inside the range can write both the chain and the viewer header and
// the resolver has no way to know; trust is by network range, so narrowing the
// range is the fix and it is a deployment property.
var loopbackAndVPC = []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8"}

func resolver(t *testing.T, trusted []string) *Resolver {
	t.Helper()
	r, err := NewResolver(trusted, "", "")
	require.NoError(t, err)
	return r
}

// edgeResolver is the cell: the same trust, plus a declared distribution in
// front of the whole deployment.
func edgeResolver(t *testing.T, trusted []string) *Resolver {
	t.Helper()
	r, err := NewResolver(trusted, "", CloudFrontViewerAddressHeader)
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
// a brand-new budget, which is not a weaker limiter but no limiter at all; on
// the security stream the same mistake lets the attacker write any address it
// likes into the evidence.
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

	var nilResolver *Resolver
	assert.Equal(t, UnknownAddress, nilResolver.ClientAddress(request("10.0.1.5:1")))
	assert.Equal(t, UnknownAddress, resolver(t, nil).ClientAddress(nil))
}

func TestNewResolver_TrustedProxyForms(t *testing.T) {
	t.Parallel()

	res, err := NewResolver([]string{" 10.0.0.0/8 ", "127.0.0.1", ""}, "", "")
	require.NoError(t, err)

	// A bare address is a single-host prefix: its neighbour is not trusted.
	assert.Equal(t, "198.51.100.77", res.ClientAddress(request("127.0.0.1:1", "198.51.100.77")))
	assert.Equal(t, "127.0.0.2", res.ClientAddress(request("127.0.0.2:1", "198.51.100.77")))
}

func TestNewResolver_RejectsNonsense(t *testing.T) {
	t.Parallel()

	_, err := NewResolver([]string{"10.0.0.0/99"}, "", "")
	require.Error(t, err)

	_, err = NewResolver([]string{"not-an-address"}, "", "")
	require.Error(t, err)
}

func TestNewResolver_CustomHeader(t *testing.T) {
	t.Parallel()

	res, err := NewResolver(loopbackAndVPC, "X-Real-IP", "")
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = "10.0.1.5:44321"
	r.Header.Set("X-Real-IP", "198.51.100.77")
	r.Header.Set(DefaultForwardedHeader, "203.0.113.9")

	assert.Equal(t, "198.51.100.77", res.ClientAddress(r),
		"only the configured header is read")
}

// PROVENANCE. The same address means different things depending on how it was
// arrived at, and a reader that cannot tell them apart cannot say which rows it
// may trust. These are the four answers, and the third is the one that has to
// be distinguishable: the load balancer standing in for a client it never
// named.
func TestResolve_SourceSaysHowTheAddressWasArrivedAt(t *testing.T) {
	t.Parallel()

	res := resolver(t, loopbackAndVPC)

	direct := res.Resolve(request("203.0.113.9:51000", "10.0.0.1"))
	assert.Equal(t, Client{Addr: "203.0.113.9", Source: FromPeer}, direct,
		"a direct caller is its socket peer, and the header it wrote is ignored")
	assert.True(t, direct.Identifies())

	proxied := res.Resolve(request("10.0.1.5:44321", "203.0.113.9, 198.51.100.77"))
	assert.Equal(t, Client{Addr: "198.51.100.77", Source: FromForwarded}, proxied,
		"believed from the header because the peer is a trusted proxy")
	assert.True(t, proxied.Identifies())

	notAppending := res.Resolve(request("10.0.1.5:44321"))
	assert.Equal(t, Client{Addr: "10.0.1.5", Source: FromProxy}, notAppending,
		"the proxy named no client: this address is our own hop, not the caller")
	assert.False(t, notAppending.Identifies(),
		"everyone behind that proxy resolves to it, so it identifies nobody")

	none := res.Resolve(request("@internal"))
	assert.Equal(t, Client{Addr: UnknownAddress, Source: FromNowhere}, none)
	assert.False(t, none.Identifies())
}

// ---- the four topologies the product is actually reached through -----------
//
// One deployment can be reached in more than one of these at once, and the
// answer has to be right in each. The addresses are documentation ranges
// throughout: 203.0.113/24 is the person at a browser, 198.51.100/24 stands for
// a public hop that is NOT the person (a CDN edge server, or an attacker), and
// 10/8 is our own private tier.
const (
	topologyViewer  = "203.0.113.9"  // the person
	topologyCDNEdge = "198.51.100.9" // the POP their bytes went through
	topologyALB     = "10.20.1.7"    // our load balancer's ENI
	topologyWeb     = "10.20.3.44"   // our web tier's ENI, the api's socket peer
	topologyForged  = "192.0.2.66"   // whatever a caller writes for itself
)

var topologyTrusted = []string{"10.20.0.0/16"}

// withEdge builds a request that carries the distribution's viewer header on
// top of whatever chain it already has.
func withEdge(r *http.Request, viewer string) *http.Request {
	r.Header.Set(CloudFrontViewerAddressHeader, viewer)
	return r
}

// TOPOLOGY 1 — DIRECT. Nothing in front: a caller opens a socket to the api
// itself. This is the self-host operator on the box, and it is also the shape
// an attacker takes when they can route to the port. The socket peer is the
// answer and everything the caller wrote — chain, viewer header, both — is
// ignored, because the peer is not a hop we named.
func TestTopology_Direct_IsTheSocketPeerAndNothingTheCallerWrote(t *testing.T) {
	t.Parallel()

	for _, res := range []*Resolver{resolver(t, topologyTrusted), edgeResolver(t, topologyTrusted)} {
		got := res.Resolve(withEdge(
			request(topologyViewer+":51000", topologyForged), topologyForged+":443"))

		assert.Equal(t, Client{Addr: topologyViewer, Source: FromPeer}, got,
			"a direct caller is the address the TCP handshake completed with")
	}
}

// TOPOLOGY 2 — LOAD BALANCER ONLY, no distribution. The self-host stack (web
// container -> api) and any plain ALB deployment. The balancer appends the peer
// it saw, so the chain is walked from the right and the first untrusted entry
// is the caller. Declaring no distribution is what keeps this path alive.
func TestTopology_LoadBalancerOnly_WalksTheChainFromTheRight(t *testing.T) {
	t.Parallel()

	res := resolver(t, topologyTrusted)

	got := res.Resolve(request(topologyWeb+":44321",
		topologyForged+", "+topologyViewer+", "+topologyALB))

	assert.Equal(t, Client{Addr: topologyViewer, Source: FromForwarded}, got,
		"our own hop is skipped and the address it appended is the caller")
	assert.NotEqual(t, topologyForged, got.Addr,
		"what the caller wrote sits to the LEFT of what the balancer appended and is never reached")
}

// TOPOLOGY 3 — DISTRIBUTION, THE ONE THE CELL SHIPS. CloudFront -> ALB -> web
// -> api. The chain that arrives is "viewer, cdnEdge, alb" (+ anything the
// viewer wrote in front), and walking it right-to-left stops at the CDN EDGE —
// a public address that is not the caller. That was the defect: every sign-in
// in the cell naming an edge server, tagged as a believed forwarded address.
// The distribution's own viewer header outranks the chain, so the person is
// recorded instead.
func TestTopology_ThroughTheDistribution_RecordsTheViewerNotTheEdge(t *testing.T) {
	t.Parallel()

	chain := topologyForged + ", " + topologyViewer + ", " + topologyCDNEdge + ", " + topologyALB

	// What the walk alone does with this chain — the bug, kept as a test so it
	// cannot come back unnoticed.
	plain := resolver(t, topologyTrusted).Resolve(request(topologyWeb+":44321", chain))
	assert.Equal(t, Client{Addr: topologyCDNEdge, Source: FromForwarded}, plain,
		"without the viewer header the walk stops at the CDN edge and calls it a caller")

	// With the distribution declared.
	got := edgeResolver(t, topologyTrusted).Resolve(withEdge(
		request(topologyWeb+":44321", chain), topologyViewer+":51000"))

	assert.Equal(t, Client{Addr: topologyViewer, Source: FromForwarded}, got,
		"the distribution's statement about the viewer is preferred over the chain")
	assert.True(t, got.Identifies())

	// CloudFront writes "address:port"; both families, both forms.
	v6 := edgeResolver(t, topologyTrusted).Resolve(withEdge(
		request(topologyWeb+":44321"), "[2001:db8::1]:51000"))
	assert.Equal(t, Client{Addr: "2001:db8::1", Source: FromForwarded}, v6)
}

// TOPOLOGY 4 — A FORGED VIEWER ADDRESS FROM SOMETHING THAT DID NOT COME THROUGH
// THE DISTRIBUTION. Two shapes, and they fail differently on purpose.
func TestTopology_ForgedViewerAddress(t *testing.T) {
	t.Parallel()

	res := edgeResolver(t, topologyTrusted)

	// (a) From outside the trusted tier — the internet, or anything that can
	// route to the port. The header buys nothing at all: the peer is not a hop
	// we named, so the socket address is the answer. This is the case that must
	// never regress, because the stream is append-only and a forged address
	// written into it can never be taken back out.
	outside := res.Resolve(withEdge(
		request(topologyForged+":51000"), topologyViewer+":443"))
	assert.Equal(t, Client{Addr: topologyForged, Source: FromPeer}, outside,
		"a viewer header from an untrusted peer is not read")

	// (b) The request came through OUR OWN tier but not through the
	// distribution — a bypass, or a distribution whose origin-request policy
	// forwards none of its own headers. The chain is NOT used as a substitute:
	// its first untrusted entry here is whatever the caller wrote. The answer
	// is our own hop, marked as our own hop.
	bypassed := res.Resolve(request(topologyWeb+":44321",
		topologyForged+", "+topologyViewer))
	assert.Equal(t, Client{Addr: topologyWeb, Source: FromProxy}, bypassed,
		"no viewer header means the request did not come through the distribution")
	assert.False(t, bypassed.Identifies(),
		"and a row that cannot name the caller must not be counted as naming one")
	assert.NotEqual(t, topologyViewer, bypassed.Addr)
	assert.NotEqual(t, topologyForged, bypassed.Addr)

	// A viewer header that is not an address is the same situation: nothing to
	// prefer, and the chain still does not become trustworthy because of it.
	garbage := res.Resolve(withEdge(
		request(topologyWeb+":44321", topologyViewer), "not-an-address"))
	assert.Equal(t, Client{Addr: topologyWeb, Source: FromProxy}, garbage)
}

// THE RESIDUAL, stated as a test so nobody has to rediscover it. Trust is by
// NETWORK RANGE: a workload inside the trusted range can write the viewer
// header and be believed, because it is indistinguishable from the tier that
// legitimately sits in front. Code cannot close this — only the deployment can,
// by naming the tier that really is in front (infra/lib/api-stack.ts passes the
// cell's private subnets rather than the whole VPC), keeping the api's security
// group closed to everything else, and refusing at the load balancer any
// request that did not come through the distribution.
func TestTopology_TrustIsByRange_SoTheRangeIsTheSecurityBoundary(t *testing.T) {
	t.Parallel()

	insider := "10.20.9.9" // inside the trusted range, not the web tier
	res := edgeResolver(t, topologyTrusted)

	got := res.Resolve(withEdge(request(insider+":40000"), topologyForged+":443"))
	assert.Equal(t, Client{Addr: topologyForged, Source: FromForwarded}, got,
		"anything inside the trusted range is believed — which is why the range must be the tier in front, and nothing more")

	// Narrow the range to the tier that really is in front and the same request
	// is attributed to the forger instead.
	narrow, err := NewResolver([]string{"10.20.3.0/24"}, "", CloudFrontViewerAddressHeader)
	require.NoError(t, err)
	assert.Equal(t, Client{Addr: insider, Source: FromPeer},
		narrow.Resolve(withEdge(request(insider+":40000"), topologyForged+":443")))
}

func TestNewResolver_EdgeViewerHeaderIsReported(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", resolver(t, topologyTrusted).EdgeViewerHeader())
	assert.Equal(t, CloudFrontViewerAddressHeader, edgeResolver(t, topologyTrusted).EdgeViewerHeader())

	var nilResolver *Resolver
	assert.Equal(t, "", nilResolver.EdgeViewerHeader())

	// Whitespace around a configured value is a deployment manifest artefact,
	// not a different header.
	padded, err := NewResolver(topologyTrusted, "", "  X-Viewer  ")
	require.NoError(t, err)
	assert.Equal(t, "X-Viewer", padded.EdgeViewerHeader())
	assert.Equal(t, Client{Addr: topologyViewer, Source: FromForwarded},
		padded.Resolve(headerRequest(topologyWeb+":1", "X-Viewer", topologyViewer)))
}

func headerRequest(remoteAddr, header, value string) *http.Request {
	r := request(remoteAddr)
	r.Header.Set(header, value)
	return r
}
