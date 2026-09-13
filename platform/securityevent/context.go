package securityevent

import (
	"context"
	"net"
	"net/netip"
)

type ctxKey string

const ctxClient ctxKey = "securityEventClient"

// How the bound address was arrived at. Mirrored by a CHECK on
// security_events.client_ip_source, and the values are the ones
// delivery/httpkit/clientaddr resolves to.
//
// WHY THE ROW CARRIES THIS AND NOT ONLY THE ADDRESS. The three cases are worth
// different amounts to an investigator and look identical once stored. A peer
// address completed a TCP handshake and cannot be forged. A forwarded one is
// worth exactly what the proxy in front of us is worth. And the third is the
// trap: the load balancer's own address, recorded because it named no client —
// a real row, about our own infrastructure, that reads like the caller. Without
// this column the only way to tell them apart is to know what the trusted-proxy
// configuration was on the day the row was written.
const (
	// AddrFromPeer — the socket peer, which is not one of our proxies.
	AddrFromPeer = "peer"
	// AddrFromForwarded — the forwarded header, believed because the socket peer
	// is a configured trusted proxy.
	AddrFromForwarded = "forwarded"
	// AddrFromProxy — the socket peer, which IS one of our proxies: it named no
	// client, so this is a hop of ours and not the caller.
	AddrFromProxy = "proxy"
)

var knownAddrSources = map[string]struct{}{
	AddrFromPeer: {}, AddrFromForwarded: {}, AddrFromProxy: {},
}

// boundClient is the caller as the transport knows it.
type boundClient struct {
	addr   string
	source string
}

// WithClient binds the caller's network address, and how the transport arrived
// at it, to the context. The address may be in whatever form the transport has
// it (net/http's RemoteAddr carries a port; a resolver hands over a bare
// address) — the recorder normalises it. An unrecognised source is dropped
// rather than stored: the column is a closed vocabulary.
//
// WHY A CONTEXT VALUE rather than a parameter on every use case. "From where"
// is a property of the REQUEST, not of the operation — the same argument that
// puts the request id and the requester in app.Context — and threading an
// address through Login, Logout, LogoutAll, MfaEnroll, MfaEnable, MfaVerify,
// AcceptInvite, AuthenticateToken and UpdateMember would change nine signatures
// to carry one ambient fact. The events themselves are still wired at the use
// cases, where a future route cannot bypass them; this only decides whether
// such a route also supplies the address.
//
// IT IS BOUND ONCE, GLOBALLY, by middlewares.ClientAddressMiddleware — not by
// each handler that happens to remember. Per-handler binding is what left
// auth.token.rejected (recorded from RequireAuth, which wraps every route) and
// the administrator-disable revocation with no address at all, while the six
// handlers that did remember carried one.
func WithClient(ctx context.Context, addr, source string) context.Context {
	if addr == "" {
		return ctx
	}
	if _, ok := knownAddrSources[source]; !ok {
		source = ""
	}
	return context.WithValue(ctx, ctxClient, boundClient{addr: addr, source: source})
}

// WithClientAddr binds an address that IS the socket peer — a direct
// connection, a test, or any transport with no notion of a proxy in front.
func WithClientAddr(ctx context.Context, addr string) context.Context {
	return WithClient(ctx, addr, AddrFromPeer)
}

// ClientAddr returns the raw address bound to the context, if any.
func ClientAddr(ctx context.Context) string {
	return clientOf(ctx).addr
}

// ClientAddrSource returns how that address was arrived at, if anything was
// bound.
func ClientAddrSource(ctx context.Context) string {
	return clientOf(ctx).source
}

func clientOf(ctx context.Context) boundClient {
	if v, ok := ctx.Value(ctxClient).(boundClient); ok {
		return v
	}
	return boundClient{}
}

// clientAddrOf is what actually reaches the INET column: the host part, in
// canonical form, or nil when there is nothing storable.
//
// The column is INET rather than text so an investigator can ask a containment
// question ("everything from 203.0.113.0/24") and so the type system, not a
// reviewer, guarantees that no caller string can land in it. That means the
// port has to go — net/http's RemoteAddr is "host:port" and inet does not take
// one — and an IPv4-mapped IPv6 address is unmapped, so the same client is one
// address in the stream however the socket happened to present it.
func clientAddrOf(ctx context.Context) *string {
	raw := ClientAddr(ctx)
	if raw == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		// Not an address at all (a hostname, the resolver's "unknown" when no
		// socket address was usable, a proxy header somebody invented).
		// Dropping it keeps the column honest; the event still lands.
		return nil
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	canonical := addr.WithZone("").String()
	return &canonical
}

// clientSourceOf is the provenance that goes with clientAddrOf. It is nil
// whenever the address is, so the two columns can never disagree about whether
// this row knows where it came from.
func clientSourceOf(ctx context.Context) *string {
	if clientAddrOf(ctx) == nil {
		return nil
	}
	source := ClientAddrSource(ctx)
	if source == "" {
		return nil
	}
	return &source
}
