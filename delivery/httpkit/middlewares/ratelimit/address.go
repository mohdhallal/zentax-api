package ratelimit

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

// UnknownAddress is the key used when no address can be determined at all. It
// is a real key, not a bypass: whatever lands there shares one budget.
const UnknownAddress = "unknown"

// AddressResolver answers "who is this request from?" for rate-limiting
// purposes, which behind a load balancer is not r.RemoteAddr.
//
// THE RULE, and why it is shaped this way. X-Forwarded-For is a header, so
// anyone can write anything in it. Believing it unconditionally does not weaken
// the limiter, it REMOVES it: an attacker sends a different fake address on
// every request and every request gets a fresh budget. Believing it never means
// that behind a load balancer every request in the cell keys to the balancer's
// own ENI address — one budget for every customer at once, so the first burst
// locks the whole tenant base out. Both failures are total, in opposite
// directions.
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
//     the attacker the limiter's key.
//  3. Anything unparseable stops the walk, and the socket peer is used. (With
//     an appending proxy this is unreachable from a forged header: the real
//     address is always to the right of whatever the client wrote.)
//
// Trust is configured, not guessed: config.RateLimitConfig.TrustedProxies, whose
// default is the loopback and private ranges a reverse proxy or a load balancer
// in the same VPC actually occupies. An explicitly empty list trusts nothing
// and pins every request to its socket address.
type AddressResolver struct {
	trusted []netip.Prefix
	header  string
}

// NewAddressResolver builds a resolver. Each entry of trustedCIDRs is a CIDR
// ("10.0.0.0/8") or a bare address ("127.0.0.1", treated as a single-host
// prefix). An empty header falls back to DefaultForwardedHeader.
func NewAddressResolver(trustedCIDRs []string, header string) (*AddressResolver, error) {
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

	return &AddressResolver{trusted: prefixes, header: strings.TrimSpace(header)}, nil
}

// Header is the forwarded header this resolver reads, for diagnostics.
func (a *AddressResolver) Header() string {
	if a == nil {
		return ""
	}
	return a.header
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

// ClientAddress returns the address the request is charged to. A caller that
// must know whether the address really identifies one client should use
// ResolveClient instead.
func (a *AddressResolver) ClientAddress(r *http.Request) string {
	addr, _ := a.ResolveClient(r)
	return addr
}

// ResolveClient returns the address the request is charged to and whether that
// address identifies a single client.
//
// It is false in exactly one case: the socket peer is a trusted proxy and the
// forwarded chain named no untrusted address, so every client behind that
// proxy resolves to the same key. Charging a per-client budget to a key shared
// by the whole installation is how one attacker's burst refuses login for
// everybody — measured: forty failed logins through the self-host adapter, and
// the next unrelated user with a correct password got a 429. The caller is
// expected to skip the per-client budget rather than share it; the per-account
// attempt budget and the concurrency bound both still apply, and the operator
// is told once that the proxy is not appending.
func (a *AddressResolver) ResolveClient(r *http.Request) (string, bool) {
	if a == nil || r == nil {
		return UnknownAddress, false
	}

	peer, ok := parseAddress(r.RemoteAddr)
	if !ok {
		// No usable socket address (a synthetic request, an unusual listener):
		// there is nothing to trust a header against, so everything such a
		// request could claim is ignored.
		return UnknownAddress, false
	}

	if !a.trusts(peer) {
		return peer.String(), true
	}

	for _, candidate := range a.forwardedChain(r) {
		addr, ok := parseAddress(candidate)
		if !ok {
			break // unparseable: attribute no further, fall back to the peer
		}
		if a.trusts(addr) {
			continue // another hop of our own infrastructure
		}
		return addr.String(), true
	}

	// Every hop was trusted, or the header was absent: the peer is the closest
	// thing to a client we have, and it is shared by everyone behind it.
	return peer.String(), false
}

// forwardedChain returns the forwarded addresses in RIGHT-TO-LEFT order —
// nearest hop first. Repeated header lines are concatenated in order, as RFC
// 9110 requires, before the reversal.
func (a *AddressResolver) forwardedChain(r *http.Request) []string {
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

func (a *AddressResolver) trusts(addr netip.Addr) bool {
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
