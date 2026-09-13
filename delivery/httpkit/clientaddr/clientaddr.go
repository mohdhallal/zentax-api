// Package clientaddr answers one question for the whole delivery layer: WHO IS
// THIS REQUEST FROM, given that behind a load balancer r.RemoteAddr is the load
// balancer.
//
// WHY IT IS ITS OWN PACKAGE. Two subsystems need the same answer and must not
// have two of them. The rate limiter needs it as a BUDGET KEY — believing a
// forged header there removes the limiter, because every request mints a fresh
// budget. The authentication event stream (ADR-0008 stream 2) needs it as
// ATTRIBUTION — "from where" is the question a breach investigation opens with,
// and in a deployed cell (CloudFront -> ALB -> web -> api) or in the self-host
// stack (web container -> api) the socket peer answers it with the name of our
// own infrastructure, on every single event. It lived in the rate-limit
// middleware first because that is where the need appeared first; it is here
// now because an event stream must not depend on a rate limiter, and because
// the resolver has to exist even when rate limiting is switched off.
//
// It is deliberately NOT in platform/: it reads an *http.Request, which is
// transport knowledge. platform/securityevent takes the ANSWER, as a string,
// bound to the context by one middleware (delivery/httpkit/middlewares), and so
// stays free of any HTTP dependency.
package clientaddr

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultForwardedHeader is the header a load balancer appends the client
// address to. AWS ALB, nginx and Traefik all use this one.
const DefaultForwardedHeader = "X-Forwarded-For"

// CloudFrontViewerAddressHeader is what AWS CloudFront calls the header it
// writes the VIEWER's address into ("198.51.100.9:53100" — address and source
// port). It is named here so a deployment can be configured with a constant
// rather than a string literal, and because it is the value the cell's CDK
// passes as RATE_LIMIT_EDGE_VIEWER_HEADER; it is NOT a default (see
// Resolver.edgeHeader). Other distributions write their own: Cloudflare
// CF-Connecting-IP, Fastly Fastly-Client-IP, Akamai True-Client-IP.
//
// It reaches this service only if TWO deployment facts hold: the distribution's
// origin-request policy forwards CloudFront's OWN headers
// (AllViewerAndCloudFrontHeaders-2022 — the plain AllViewer policy does not),
// and every reverse proxy between the distribution and this service passes it
// on. Neither is a code property of this repository, and Resolve refuses to
// paper over the result when they do not hold.
const CloudFrontViewerAddressHeader = "CloudFront-Viewer-Address"

// UnknownAddress is the key used when no address can be determined at all. It
// is a real key, not a bypass: whatever lands there shares one budget.
const UnknownAddress = "unknown"

// How an address was arrived at. A reader that cannot tell a TCP-verified peer
// from a header a proxy wrote — or from our own load balancer standing in for a
// client it never named — cannot say which rows it may trust, so every answer
// carries its provenance and the security stream stores it alongside the
// address (platform/securityevent, security_events.client_ip_source).
const (
	// FromPeer — the address IS the socket peer, and the socket peer is not one
	// of our proxies. Unforgeable: the TCP handshake completed with it.
	FromPeer = "peer"
	// FromForwarded — taken from the forwarded header, believed because the
	// socket peer is a configured trusted proxy. As trustworthy as that proxy.
	FromForwarded = "forwarded"
	// FromProxy — the socket peer, which IS one of our proxies: the chain named
	// no client, so this address is a hop of our own infrastructure and not the
	// caller. The row is real; reading it as "where this came from" is the
	// mistake this value exists to prevent.
	FromProxy = "proxy"
	// FromNowhere — there was no usable socket address at all (a synthetic
	// request, an unusual listener). Addr is UnknownAddress.
	FromNowhere = "nowhere"
)

// Client is one resolved caller: the address, and how the resolver arrived at
// it.
type Client struct {
	// Addr is the address the request is attributed to, without a port, or
	// UnknownAddress.
	Addr string
	// Source is one of the constants above.
	Source string
}

// Identifies reports whether Addr identifies a single client.
//
// It is false in exactly two cases: nothing was resolvable at all, and the
// socket peer is a trusted proxy whose forwarded chain named no untrusted
// address — so every client behind that proxy resolves to the same value.
// Charging a per-client rate-limit budget to a key shared by the whole
// installation is how one attacker's burst refuses login for everybody —
// measured: forty failed logins through the self-host adapter, and the next
// unrelated user with a correct password got a 429. The caller is expected to
// skip the per-client budget rather than share it; the per-account attempt
// budget and the concurrency bound both still apply, and the operator is told
// once that the proxy is not appending.
func (c Client) Identifies() bool {
	return c.Source == FromPeer || c.Source == FromForwarded
}

// Resolver answers "who is this request from?", which behind a load balancer is
// not r.RemoteAddr.
//
// THE RULE, and why it is shaped this way. X-Forwarded-For is a header, so
// anyone can write anything in it. Believing it unconditionally does not weaken
// the rate limiter, it REMOVES it: an attacker sends a different fake address on
// every request and every request gets a fresh budget. Believing it never means
// that behind a load balancer every request in the cell keys to the balancer's
// own ENI address — one budget for every customer at once, so the first burst
// locks the whole tenant base out. Both failures are total, in opposite
// directions. For the security stream the same two failures read as: an
// attacker who can write any address into the evidence, or evidence that names
// the load balancer on every row.
//
// So the header is believed exactly as far as the network is trustworthy:
//
//  1. The SOCKET peer (r.RemoteAddr) must itself be a trusted proxy. If the
//     request arrived from anywhere else the header is ignored outright — this
//     is what makes the address unspoofable from the internet.
//  2. The forwarded chain is then walked RIGHT TO LEFT, skipping entries that
//     are themselves trusted proxies. The first untrusted address from the
//     right is the client. This is the half that matters: a proxy APPENDS the
//     peer it actually saw, so an attacker who sends
//     "X-Forwarded-For: 1.2.3.4" arrives as "1.2.3.4, <their real address>"
//     and the walk picks their real address, never the forgery. Reading the
//     leftmost entry — the naive implementation, and the common one — hands
//     the attacker the limiter's key and the investigator's evidence.
//  3. Anything unparseable stops the walk, and the socket peer is used. (With
//     an appending proxy this is unreachable from a forged header: the real
//     address is always to the right of whatever the client wrote.)
//
// Trust is configured, not guessed: config.RateLimitConfig.TrustedProxies, whose
// default is empty — no forwarded header is believed until a deployment names
// the hops in front of it. An explicitly empty list trusts nothing and pins
// every request to its socket address.
//
// WHERE THE WALK IS NOT ENOUGH: A CONTENT-DELIVERY DISTRIBUTION. The rule above
// holds while the LEFTMOST hop our own infrastructure appends is the caller. In
// the cell it is not. The chain that reaches the api is
//
//	viewer-written…, viewerAddress, cdnEdgeAddress, loadBalancerAddress
//
// because CloudFront appends the viewer and the ALB then appends the CloudFront
// POP it was talking to. Walking right to left, the load balancer is ours and is
// skipped, and the walk stops at the CDN edge — a public address that is not the
// caller and is not even stable between two requests of one session. Every
// security event in the cell would name an edge server, tagged "forwarded",
// which reads as a genuine caller. Trusting the CDN's own ranges instead would
// work, but they are an AWS-managed prefix list of ~100 entries that changes
// without us, and a stale copy silently reintroduces exactly this bug.
//
// So when a deployment sits behind a distribution it names the header the
// distribution writes the viewer into (edgeHeader, from
// RATE_LIMIT_EDGE_VIEWER_HEADER). That header is the one thing in the request
// the VIEWER cannot forge: CloudFront strips any client-supplied CloudFront-*
// header and writes its own. It is preferred over the chain, and — this is the
// half that is not an optimisation — when it is ABSENT the chain is not used as
// a substitute. A deployment that declares a distribution is declaring that
// every legitimate request comes through it, so a request without the header
// did not, and its chain's first untrusted entry is an edge server or a forgery
// either way. Such a request resolves to the socket peer tagged FromProxy: "our
// own hop, NOT the caller", the value the stream already has for exactly this,
// so the degradation is visible in security_events.client_ip_source (and in a
// one-time warning from the middleware) instead of being a plausible-looking
// address nobody can tell apart. See delivery/httpkit/middlewares/client_addr.go.
//
// WHAT THIS DOES NOT FIX, because no code here can. Trust is by NETWORK RANGE,
// so any workload inside the trusted range can write both the chain and the
// edge header and be believed — it is indistinguishable from the tier that
// legitimately sits in front. Three deployment properties, not code properties,
// are what actually bound that:
//
//	the trusted range names the tier in front and nothing else
//	  (infra/lib/api-stack.ts passes the cell's PRIVATE subnets, not the VPC);
//	the api's security group admits only that tier;
//	the origin refuses any request that did not come through the distribution
//	  (the ALB answers 403 without the origin-verify header CloudFront sends).
type Resolver struct {
	trusted []netip.Prefix
	header  string
	// edgeHeader is the header a content-delivery distribution in front of the
	// whole deployment writes the viewer's address into. EMPTY means "no
	// distribution declared" and the resolver behaves exactly as it did before
	// this existed — which is the right answer for the self-host stack, for a
	// bare load balancer, and for every test that is not about a CDN.
	edgeHeader string
}

// NewResolver builds a resolver. Each entry of trustedCIDRs is a CIDR
// ("10.0.0.0/8") or a bare address ("127.0.0.1", treated as a single-host
// prefix). An empty header falls back to DefaultForwardedHeader.
//
// edgeViewerHeader is the header a content-delivery distribution in front of
// the deployment writes the viewer's address into (CloudFrontViewerAddressHeader
// in a cell). Empty — the only sensible default — means no distribution is
// declared and the forwarded chain is the whole answer; see Resolver.
func NewResolver(trustedCIDRs []string, header, edgeViewerHeader string) (*Resolver, error) {
	if strings.TrimSpace(header) == "" {
		header = DefaultForwardedHeader
	}

	prefixes := make([]netip.Prefix, 0, len(trustedCIDRs))
	for _, raw := range trustedCIDRs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		prefix, err := ParsePrefix(raw)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}

	return &Resolver{
		trusted:    prefixes,
		header:     strings.TrimSpace(header),
		edgeHeader: strings.TrimSpace(edgeViewerHeader),
	}, nil
}

// Header is the forwarded header this resolver reads, for diagnostics.
func (a *Resolver) Header() string {
	if a == nil {
		return ""
	}
	return a.header
}

// EdgeViewerHeader is the distribution's viewer header this resolver prefers,
// or "" when no distribution is declared. The middleware reads it to tell a
// deployment that has one from a deployment that has not, which is the
// difference between "the chain is the answer" and "the chain is not to be
// trusted as one".
func (a *Resolver) EdgeViewerHeader() string {
	if a == nil {
		return ""
	}
	return a.edgeHeader
}

// ParsePrefix parses a trusted-proxy entry: a CIDR or a bare IP address. It is
// exported so configuration can be validated at startup rather than at the
// first request.
func ParsePrefix(raw string) (netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "/") {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("trusted proxy %q is not a valid CIDR: %w", raw, err)
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("trusted proxy %q is not a valid IP address or CIDR: %w", raw, err)
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// ClientAddress returns just the address a request is attributed to. A caller
// that must know how much that address is worth uses Resolve.
func (a *Resolver) ClientAddress(r *http.Request) string {
	return a.Resolve(r).Addr
}

// Resolve returns the address the request is attributed to and how it was
// arrived at.
func (a *Resolver) Resolve(r *http.Request) Client {
	if a == nil || r == nil {
		return Client{Addr: UnknownAddress, Source: FromNowhere}
	}

	peer, ok := parseAddress(r.RemoteAddr)
	if !ok {
		// No usable socket address (a synthetic request, an unusual listener):
		// there is nothing to trust a header against, so everything such a
		// request could claim is ignored.
		return Client{Addr: UnknownAddress, Source: FromNowhere}
	}

	if !a.trusts(peer) {
		return Client{Addr: peer.String(), Source: FromPeer}
	}

	// A DISTRIBUTION IS DECLARED: its statement about the viewer outranks the
	// chain, and its absence is not something to walk around. See Resolver.
	if a.edgeHeader != "" {
		if addr, ok := parseAddress(r.Header.Get(a.edgeHeader)); ok {
			return Client{Addr: addr.String(), Source: FromForwarded}
		}
		return Client{Addr: peer.String(), Source: FromProxy}
	}

	for _, candidate := range a.forwardedChain(r) {
		addr, ok := parseAddress(candidate)
		if !ok {
			break // unparseable: attribute no further, fall back to the peer
		}
		if a.trusts(addr) {
			continue // another hop of our own infrastructure
		}
		return Client{Addr: addr.String(), Source: FromForwarded}
	}

	// Every hop was trusted, or the header was absent: the peer is the closest
	// thing to a client we have, and it is shared by everyone behind it.
	return Client{Addr: peer.String(), Source: FromProxy}
}

// forwardedChain returns the forwarded addresses in RIGHT-TO-LEFT order —
// nearest hop first. Repeated header lines are concatenated in order, as RFC
// 9110 requires, before the reversal.
func (a *Resolver) forwardedChain(r *http.Request) []string {
	values := r.Header.Values(a.header)
	if len(values) == 0 {
		return nil
	}

	var chain []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				chain = append(chain, part)
			}
		}
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

func (a *Resolver) trusts(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range a.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// parseAddress accepts the forms an address reaches us in: bare ("1.2.3.4",
// "2001:db8::1"), host:port as r.RemoteAddr always is ("1.2.3.4:5678",
// "[2001:db8::1]:5678") and the bracketed form some proxies write. An
// IPv4-mapped IPv6 address is unmapped, so ::ffff:1.2.3.4 and 1.2.3.4 are one
// key rather than two budgets.
func parseAddress(raw string) (netip.Addr, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}, false
	}

	if addr, err := netip.ParseAddr(raw); err == nil {
		return addr.Unmap(), true
	}
	if addrPort, err := netip.ParseAddrPort(raw); err == nil {
		return addrPort.Addr().Unmap(), true
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
			return addr.Unmap(), true
		}
	}
	if addr, err := netip.ParseAddr(strings.Trim(raw, "[]")); err == nil {
		return addr.Unmap(), true
	}
	return netip.Addr{}, false
}
