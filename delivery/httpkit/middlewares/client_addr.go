package middlewares

import (
	"net/http"
	"sync"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/clientaddr"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/securityevent"
)

// ClientAddressMiddleware resolves WHO THE CALLER IS — through the configured
// trusted-proxy chain, not from the socket alone — and binds the answer to the
// request context, once, for everything downstream.
//
// WHY IT IS GLOBAL AND NOT PER HANDLER. The address used to be bound by each
// /auth handler that remembered to, which produced exactly the gaps that shape
// predicts: auth.token.rejected is recorded from RequireAuth, which wraps EVERY
// route rather than only identity's, so a rejected API token — "where was this
// stolen credential being presented from" — carried no address at all; and the
// administrator-disable revocation, recorded from the members use case, carried
// none either. An ambient property of the request belongs to the request, bound
// once where every route passes, so a new route inherits it instead of having to
// know about it.
//
// WHY IT RESOLVES RATHER THAN READING r.RemoteAddr. The API has no public
// surface in either edition — CloudFront -> ALB -> web -> api in a cell, the web
// container -> api in the self-host stack — so the socket peer is a hop of our
// own infrastructure on every request. Recording that as "from where" makes the
// security stream unanswerable in precisely the deployments it exists for. The
// resolver (delivery/httpkit/clientaddr) is the same one the rate limiter keys
// its budgets on, and it is the reason a forwarded header is safe to read here:
// it believes one only from a socket peer the operator named as a trusted proxy,
// and then walks the chain from the right, so a forged X-Forwarded-For yields
// the forger's own address rather than the one they wrote.
//
// It binds the PROVENANCE with the address, because the three answers are worth
// different amounts and look identical once stored — see securityevent's
// AddrFrom* constants and security_events.client_ip_source.
//
// A resolver is always built by the composition root, INDEPENDENTLY of the rate
// limiter (which does not exist when rate limiting is switched off). A nil one
// binds nothing: every event then records the address it would have recorded
// before this middleware existed, which is none.
//
// WHEN A DISTRIBUTION IS DECLARED AND DOES NOT ANSWER. In a cell the request
// reaches us through a CDN and then a load balancer, and the only unforgeable
// statement about the viewer is the header the CDN writes
// (RATE_LIMIT_EDGE_VIEWER_HEADER). A request that arrives without it did not
// come through the CDN, so the resolver refuses to substitute the forwarded
// chain — whose first untrusted entry in that topology is a CDN edge server or
// a forgery — and answers FromProxy instead. The row then says "our own hop,
// not the caller", which is honest but is also a deployment fault: the
// distribution's origin-request policy is not forwarding its own headers, or
// something is reaching the api around it. The operator is told, ONCE per
// application — the fault is a property of the deployment, not of the request,
// and a line per login would be a denial of service against its own log.
func ClientAddressMiddleware(resolver *clientaddr.Resolver) func(http.Handler) http.Handler {
	var noEdgeHeaderOnce sync.Once
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			client := resolver.Resolve(r)
			if edge := resolver.EdgeViewerHeader(); edge != "" && client.Source == clientaddr.FromProxy {
				warnMissingEdgeViewerHeader(&noEdgeHeaderOnce, r, edge, client.Addr)
			}
			if source, ok := addrSource(client.Source); ok {
				next.ServeHTTP(w, r.WithContext(
					securityevent.WithClient(r.Context(), client.Addr, source)))
				return
			}
			// Nothing resolvable (a synthetic request, an unusual listener).
			// "unknown" is a real rate-limit key but it is not an address, and a
			// column that holds addresses must not be handed one.
			next.ServeHTTP(w, r)
		})
	}
}

// warnMissingEdgeViewerHeader names the deployment fault and what it costs, so
// that "every row says proxy" is diagnosable from the logs rather than from
// first principles three months later.
func warnMissingEdgeViewerHeader(once *sync.Once, r *http.Request, header, peer string) {
	once.Do(func() {
		logger.Log.WithContext(r.Context()).Warn(
			"attribution: requests are arriving without the distribution's viewer header",
			logger.String("reason", "a content-delivery distribution is declared but its header is absent, so this request did not come through it"),
			logger.String("edgeViewerHeader", header),
			logger.String("peer", peer),
			logger.String("effect", "the caller cannot be named: these events record our own hop with client_ip_source=proxy"),
			logger.String("fix", "forward the distribution's own headers to the origin (CloudFront: the AllViewerAndCloudFrontHeaders-2022 origin-request policy), or unset RATE_LIMIT_EDGE_VIEWER_HEADER if nothing sits in front"),
		)
	})
}

// addrSource translates the transport's vocabulary into the stream's. The two
// are deliberately separate: one describes how an HTTP request was resolved, the
// other is a value a CHECK constraint pins in an append-only table, and a silent
// rename on either side would otherwise write a column value nothing can read.
func addrSource(source string) (string, bool) {
	switch source {
	case clientaddr.FromPeer:
		return securityevent.AddrFromPeer, true
	case clientaddr.FromForwarded:
		return securityevent.AddrFromForwarded, true
	case clientaddr.FromProxy:
		return securityevent.AddrFromProxy, true
	default:
		return "", false
	}
}
