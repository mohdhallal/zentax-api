package securityevents_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/google/uuid"

	acceptconfig "github.com/mohamadhallal/zentax-api/acceptance/config"
	"github.com/mohamadhallal/zentax-api/bootstrap"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/clientaddr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/platform/securityevent"
)

// Addresses from the documentation ranges, so they can never collide with
// anything real: one stands for a caller, one for a second hop, one for the
// edge server of a content-delivery distribution — a public address that is
// ours in no sense and is not the caller either.
const (
	addrClient   = "203.0.113.9"
	addrUpstream = "198.51.100.77"
	addrCDNEdge  = "198.51.100.9"
	addrForged   = "1.2.3.4"
)

// THE FORGERY. The caller writes its own X-Forwarded-For and reaches the API
// directly — which is not hypothetical: the self-host edition publishes the API
// port, and inside a cell anything that can route to the task can connect to it.
//
// The header is ignored outright, because the socket peer is not a configured
// trusted proxy, and the row says PEER so a reader knows the address is the one
// the TCP handshake completed with rather than one somebody typed. If this ever
// regresses, the security stream becomes a place where an attacker can write
// any address they like into the evidence — and it is append-only, so the
// forgery could never be taken back out.
func (s *SecuritySuite) TestAForgedForwardedHeaderFromAnUntrustedPeerIsNotBelieved() {
	stranger := "forger-" + uuid.NewString() + "@example.invalid"

	status := s.loginAt(s.External.URL, stranger, map[string]string{
		"X-Forwarded-For": addrClient,
	})
	s.Require().Equal(http.StatusUnauthorized, status)

	entries := s.stream(securityevent.Filter{SubjectDigest: s.digestOf(stranger)})
	s.Require().Len(entries, 1, "%v", s.eventsOf(entries))

	got := entries[0]
	s.Require().Equal("127.0.0.1", got.ClientIP.String,
		"the peer is the answer; a header from an untrusted peer buys nothing")
	s.Require().NotEqual(addrClient, got.ClientIP.String,
		"a caller that could name its own address could write any address into the evidence")
	s.Require().Equal(securityevent.AddrFromPeer, got.ClientIPSource.String)
}

// THE DEPLOYMENT THAT ACTUALLY SHIPS. The API has no public surface in either
// edition — CloudFront -> ALB -> web -> api in a cell, the web container -> api
// in the self-host stack — so the socket peer is a hop of our own on every
// request and "from where" would name our own infrastructure on every row.
//
// With the proxy named as trusted, the real client is recorded, the row says
// FORWARDED, and the forgery in front of it still buys nothing: a proxy APPENDS
// the peer it saw, so the chain is walked from the right and the attacker's own
// address is the first untrusted entry. Reading the LEFTMOST entry — the common
// implementation — would record "1.2.3.4" here.
//
// Rate limiting is OFF in this application on purpose: the resolver is shared
// with the limiter but must not depend on it, and a deployment that switched the
// limiter off used to be one where the stream silently went back to naming the
// load balancer.
func (s *SecuritySuite) TestTheRealClientIsRecordedThroughATrustedProxy() {
	url := s.proxiedAPI()

	direct := "proxied-" + uuid.NewString() + "@example.invalid"
	s.Require().Equal(http.StatusUnauthorized, s.loginAt(url, direct, map[string]string{
		"X-Forwarded-For": addrClient,
	}))

	entries := s.stream(securityevent.Filter{SubjectDigest: s.digestOf(direct)})
	s.Require().Len(entries, 1, "%v", s.eventsOf(entries))
	s.Require().Equal(addrClient, entries[0].ClientIP.String,
		"behind a trusted proxy the client is what the proxy appended, not the socket peer")
	s.Require().Equal(securityevent.AddrFromForwarded, entries[0].ClientIPSource.String)

	// The same route, with a forged entry in front of the real one.
	forged := "forged-behind-proxy-" + uuid.NewString() + "@example.invalid"
	s.Require().Equal(http.StatusUnauthorized, s.loginAt(url, forged, map[string]string{
		"X-Forwarded-For": addrForged + ", " + addrUpstream,
	}))

	entries = s.stream(securityevent.Filter{SubjectDigest: s.digestOf(forged)})
	s.Require().Len(entries, 1, "%v", s.eventsOf(entries))
	s.Require().Equal(addrUpstream, entries[0].ClientIP.String,
		"the chain is walked from the right: the address the proxy appended wins")
	s.Require().NotEqual(addrForged, entries[0].ClientIP.String)

	// And the case an investigator must be able to see for what it is: a trusted
	// proxy that forwarded nothing. The address is real and it is OURS — reading
	// it as the caller is the mistake the provenance column exists to prevent.
	silent := "silent-proxy-" + uuid.NewString() + "@example.invalid"
	s.Require().Equal(http.StatusUnauthorized, s.loginAt(url, silent, nil))

	entries = s.stream(securityevent.Filter{SubjectDigest: s.digestOf(silent)})
	s.Require().Len(entries, 1, "%v", s.eventsOf(entries))
	s.Require().Equal("127.0.0.1", entries[0].ClientIP.String)
	s.Require().Equal(securityevent.AddrFromProxy, entries[0].ClientIPSource.String,
		"the proxy named no client, and the row has to say so rather than pass its own address off as one")

	// The same resolution feeds sessions.ip, which was wrong for exactly the same
	// reason: a tenant asking "where are my sessions open from" was answered with
	// the address of our own web tier, once per session in the installation.
	tenant := s.InsertTenant("sec-proxy-session", "Security Proxy Session")
	userID := s.InsertUserWithPassword(tenant, "proxy-session@acme.com", "s3cret-password")
	s.Require().Equal(http.StatusOK, s.postAt(url, "/auth/login",
		map[string]any{"email": "proxy-session@acme.com", "password": "s3cret-password"},
		map[string]string{"X-Forwarded-For": addrClient}))

	var sessionIP string
	s.Require().NoError(s.DB.QueryRowx(
		`SELECT ip FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&sessionIP))
	s.Require().Equal(addrClient, sessionIP,
		"the session records the client, not the hop it arrived through")
}

// THE ONE PUBLIC ROUTE THAT ESTABLISHES A CREDENTIAL. /auth/accept-invite turns
// an account that cannot sign in into one that can, without a session, without a
// tenant, and answering every failure with the same uninformative 400. It used
// to record nothing at all in either store: a redemption could be placed at no
// address, and somebody working through invite tokens left no trace anywhere.
//
// Now the redemption and each class of refusal are on the stream, the address
// they came from is the caller's, and nothing derived from the token is stored —
// an append-only ledger cannot unpublish a verifier for a live credential later.
func (s *SecuritySuite) TestInviteRedemptionAndItsRefusalsAreRecorded() {
	tenant := s.InsertTenant("sec-invite", "Security Invite").String()
	admin := s.As(tenant)

	var issued struct {
		Member struct {
			ID string `json:"id"`
		} `json:"member"`
		InviteToken string `json:"inviteToken"`
	}
	created := admin.POST(s.T(), "/members", map[string]any{
		"email": "invitee-" + uuid.NewString() + "@acme.com", "name": "Invitee", "role": "preparer",
	})
	created.AssertStatus(s.T(), http.StatusCreated)
	created.DecodeData(s.T(), &issued)
	s.Require().True(strings.HasPrefix(issued.InviteToken, "zti_"))

	url := s.proxiedAPI()
	header := map[string]string{"X-Forwarded-For": addrClient}

	// A token that is not even shaped like ours, and a well-formed one that
	// matches no row: both are somebody guessing, and both are invisible outside
	// this stream — no session, no audit entry, one generic 400.
	s.Require().Equal(http.StatusBadRequest,
		s.acceptInviteAt(url, "not-an-invite-token-at-all", header))
	s.Require().Equal(http.StatusBadRequest,
		s.acceptInviteAt(url, "zti_"+uuid.NewString()+uuid.NewString(), header))

	refusals := s.stream(securityevent.Filter{Event: securityevent.EventInviteRejected})
	s.Require().Len(refusals, 2, "%v", s.eventsOf(refusals))
	for _, got := range refusals {
		s.Require().Equal(securityevent.OutcomeFailure, got.Outcome)
		s.Require().Equal(securityevent.MethodInviteToken, got.Method)
		s.Require().Equal(securityevent.ReasonUnknownCredential, got.Reason.String)
		s.Require().False(got.PrincipalID.Valid, "a token that matched nothing names nobody")
		s.Require().False(got.TenantID.Valid, "and belongs to no tenant — the row audit_log cannot hold")
		s.Require().Equal(addrClient, got.ClientIP.String,
			"the address is the only correlator a refused redemption has")
	}

	// The redemption itself.
	s.Require().Equal(http.StatusOK, s.acceptInviteAt(url, issued.InviteToken, header))

	accepted := s.stream(securityevent.Filter{Event: securityevent.EventInviteAccepted})
	s.Require().Len(accepted, 1, "%v", s.eventsOf(accepted))
	s.Require().Equal(securityevent.OutcomeSuccess, accepted[0].Outcome)
	s.Require().Equal(securityevent.MethodInviteToken, accepted[0].Method)
	s.Require().Equal(tenant, accepted[0].TenantID.String)
	s.Require().Equal(issued.Member.ID, accepted[0].PrincipalID.String,
		"the stream must name the account that became able to sign in")
	s.Require().Equal(addrClient, accepted[0].ClientIP.String)
	s.Require().Equal(securityevent.AddrFromForwarded, accepted[0].ClientIPSource.String)

	// Nothing derived from the credential reaches the row — checked against the
	// whole row as text, not the columns the reader selects.
	raw := s.rawRow(accepted[0].EventID)
	s.Require().NotContains(raw, strings.TrimPrefix(issued.InviteToken, "zti_"),
		"an append-only ledger must never hold a verifier for a credential")

	// The same token again: real, and spent. Told apart from a guess on purpose —
	// one is a person who needs a new invitation, the other is an attacker.
	s.Require().Equal(http.StatusBadRequest, s.acceptInviteAt(url, issued.InviteToken, header))

	spent := s.stream(securityevent.Filter{Event: securityevent.EventInviteRejected})
	s.Require().Len(spent, 3, "%v", s.eventsOf(spent))
	s.Require().Equal(securityevent.ReasonCredentialExpired, spent[0].Reason.String)
	s.Require().Equal(issued.Member.ID, spent[0].PrincipalID.String,
		"a spent invitation still names whose it was")
	s.Require().Equal(addrClient, spent[0].ClientIP.String)
}

// THE EVENTS NO HANDLER BOUND AN ADDRESS FOR. Both of these are recorded from
// use cases that no /auth handler is on the path of: a rejected bearer token
// comes through RequireAuth, which wraps EVERY route, and the revocation comes
// from the members administration route. While the address was bound by the six
// handlers that remembered to, both rows carried none — so "where was this
// stolen API token being presented from" and "which administrator, from where,
// killed this account's credentials" both came back empty.
func (s *SecuritySuite) TestEveryEventCarriesAnAddressIncludingTheOnesNoAuthHandlerServes() {
	tenant := s.InsertTenant("sec-address", "Security Address").String()
	admin := s.As(tenant)

	s.Client.External().WithBearer("ztx_"+uuid.NewString()+uuid.NewString()).
		GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusUnauthorized)

	rejected := s.stream(securityevent.Filter{Event: securityevent.EventTokenRejected})
	s.Require().Len(rejected, 1)
	s.Require().Equal("127.0.0.1", rejected[0].ClientIP.String,
		"a rejected credential without an address answers half the question")
	s.Require().Equal(securityevent.AddrFromPeer, rejected[0].ClientIPSource.String)

	var issued struct {
		Member struct {
			ID string `json:"id"`
		} `json:"member"`
	}
	created := admin.POST(s.T(), "/members", map[string]any{
		"email": "leaver-" + uuid.NewString() + "@acme.com", "name": "Leaver", "role": "preparer",
	})
	created.AssertStatus(s.T(), http.StatusCreated)
	created.DecodeData(s.T(), &issued)

	admin.PUT(s.T(), "/members/"+issued.Member.ID, map[string]any{"name": "Leaver", "status": "disabled"}).
		AssertStatus(s.T(), http.StatusOK)

	revoked := s.stream(securityevent.Filter{Event: securityevent.EventSessionsRevoked})
	s.Require().Len(revoked, 1)
	s.Require().Equal(securityevent.ReasonMemberDisabled, revoked[0].Reason.String)
	s.Require().Equal("127.0.0.1", revoked[0].ClientIP.String,
		"an administrator ending somebody's access must be placeable at an address")
	s.Require().Equal(securityevent.AddrFromPeer, revoked[0].ClientIPSource.String)
}

// THE TOPOLOGY THE CELL IS ACTUALLY REACHED THROUGH: a content-delivery
// distribution, then a load balancer, then the web tier, then the api.
//
// The chain that arrives at the api ends "…, viewer, cdnEdge, loadBalancer",
// because the distribution appends the viewer and the load balancer then
// appends the DISTRIBUTION's edge server. Walking it from the right skips our
// own hop and stops at the edge server — a public address that is not the
// person, is not stable across one session, and reads as a believed client
// address. Every sign-in in production would have named a POP.
//
// So the distribution states the viewer in a header of its own, which is the
// one part of the request the viewer cannot write (CloudFront strips
// client-supplied CloudFront-* headers and writes its own), and that outranks
// the chain. The first half of this test is the DEFECT, kept as a control on
// the same request, so the two answers stand next to each other in the store.
func (s *SecuritySuite) TestTheDistributionsViewerIsRecordedRatherThanItsEdgeServer() {
	// As it reaches the api: what the viewer wrote, the viewer the CDN appended,
	// the POP the load balancer appended. (The httptest client is the last hop.)
	chain := addrForged + ", " + addrClient + ", " + addrCDNEdge

	// CONTROL — the same request at a deployment with no distribution declared.
	// The walk stops at the edge server and calls it a caller.
	edgeNamed := "edge-named-" + uuid.NewString() + "@example.invalid"
	s.Require().Equal(http.StatusUnauthorized, s.loginAt(s.proxiedAPI(), edgeNamed, map[string]string{
		"X-Forwarded-For": chain,
	}))

	entries := s.stream(securityevent.Filter{SubjectDigest: s.digestOf(edgeNamed)})
	s.Require().Len(entries, 1, "%v", s.eventsOf(entries))
	s.Require().Equal(addrCDNEdge, entries[0].ClientIP.String,
		"the chain walk alone names the distribution's edge server")
	s.Require().Equal(securityevent.AddrFromForwarded, entries[0].ClientIPSource.String,
		"and tags it as a believed client address, which is what made it unanswerable")

	// THE FIX — the same chain, at a deployment that declares its distribution.
	url := s.distributedAPI(loopbackTrusted)
	viewer := "viewer-" + uuid.NewString() + "@example.invalid"
	s.Require().Equal(http.StatusUnauthorized, s.loginAt(url, viewer, map[string]string{
		"X-Forwarded-For":                        chain,
		clientaddr.CloudFrontViewerAddressHeader: addrClient + ":51000",
	}))

	entries = s.stream(securityevent.Filter{SubjectDigest: s.digestOf(viewer)})
	s.Require().Len(entries, 1, "%v", s.eventsOf(entries))
	s.Require().Equal(addrClient, entries[0].ClientIP.String,
		"the person, not the edge server their bytes went through")
	s.Require().NotEqual(addrCDNEdge, entries[0].ClientIP.String)
	s.Require().NotEqual(addrForged, entries[0].ClientIP.String,
		"and not what the viewer wrote for itself, which the distribution overwrites")
	s.Require().Equal(securityevent.AddrFromForwarded, entries[0].ClientIPSource.String)

	// The same resolution feeds sessions.ip: "where are my sessions open from"
	// must answer with the person too, not with a POP.
	tenant := s.InsertTenant("sec-edge-session", "Security Edge Session")
	userID := s.InsertUserWithPassword(tenant, "edge-session@acme.com", "s3cret-password")
	s.Require().Equal(http.StatusOK, s.postAt(url, "/auth/login",
		map[string]any{"email": "edge-session@acme.com", "password": "s3cret-password"},
		map[string]string{
			"X-Forwarded-For":                        chain,
			clientaddr.CloudFrontViewerAddressHeader: addrClient + ":51000",
		}))

	var sessionIP string
	s.Require().NoError(s.DB.QueryRowx(
		`SELECT ip FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&sessionIP))
	s.Require().Equal(addrClient, sessionIP)
}

// A FORGED VIEWER ADDRESS, in the two shapes it can arrive in — and they fail
// differently on purpose.
//
// The header is only worth anything because of where the request came FROM.
// From outside the tier in front of the api it is not read at all. From inside
// it, its ABSENCE is the tell: a deployment that declares a distribution is
// declaring that every legitimate request comes through it, so a request
// without the header did not, and its chain is not a substitute — the chain's
// first untrusted entry there is an edge server or a forgery either way. Such a
// request is recorded as OUR OWN HOP, which the stream has a value for, instead
// of as a plausible address nobody can tell from a caller. The fallback is
// visible in the row rather than silent.
func (s *SecuritySuite) TestAForgedViewerAddressBuysNothingAndTheFallbackSaysSo() {
	// (a) Not through anything of ours: the socket peer is the answer and every
	// header the caller wrote — chain and viewer alike — is ignored.
	outside := "viewer-forger-" + uuid.NewString() + "@example.invalid"
	s.Require().Equal(http.StatusUnauthorized,
		s.loginAt(s.distributedAPI([]string{"10.0.0.0/8"}), outside, map[string]string{
			"X-Forwarded-For":                        addrClient,
			clientaddr.CloudFrontViewerAddressHeader: addrClient + ":443",
		}))

	entries := s.stream(securityevent.Filter{SubjectDigest: s.digestOf(outside)})
	s.Require().Len(entries, 1, "%v", s.eventsOf(entries))
	s.Require().Equal("127.0.0.1", entries[0].ClientIP.String,
		"a viewer header from a peer that is not one of our hops buys nothing")
	s.Require().NotEqual(addrClient, entries[0].ClientIP.String)
	s.Require().Equal(securityevent.AddrFromPeer, entries[0].ClientIPSource.String)

	// (b) Through our own tier but NOT through the distribution — a bypass, or a
	// distribution configured to forward none of its own headers. The chain is
	// refused as a substitute and the row says "this is our hop, not the
	// caller".
	bypassed := "distribution-bypass-" + uuid.NewString() + "@example.invalid"
	s.Require().Equal(http.StatusUnauthorized,
		s.loginAt(s.distributedAPI(loopbackTrusted), bypassed, map[string]string{
			"X-Forwarded-For": addrForged + ", " + addrClient,
		}))

	entries = s.stream(securityevent.Filter{SubjectDigest: s.digestOf(bypassed)})
	s.Require().Len(entries, 1, "%v", s.eventsOf(entries))
	s.Require().Equal("127.0.0.1", entries[0].ClientIP.String)
	s.Require().Equal(securityevent.AddrFromProxy, entries[0].ClientIPSource.String,
		"the degradation has to be readable in the row: a caller we could not name")
	s.Require().NotEqual(addrClient, entries[0].ClientIP.String,
		"the chain must not stand in for the distribution's statement")
	s.Require().NotEqual(addrForged, entries[0].ClientIP.String)
}

// loopbackTrusted is what "the tier in front of the api" is in a test: the
// httptest client itself stands in for the web task. In a cell it is the
// private subnets the web tasks run in (infra/lib/api-stack.ts) — NOT the whole
// VPC, because trust is by network range and everything inside it can write any
// address into a header.
var loopbackTrusted = []string{"127.0.0.0/8", "::1/128"}

// proxiedAPI starts a second external API that TRUSTS THE LOOPBACK, so the
// httptest server's own client stands in for the load balancer and these tests
// can present a client address exactly as the ALB does. Rate limiting is left
// off: the address resolver is shared with the limiter, and it has to be built
// whether or not the limiter exists.
func (s *SecuritySuite) proxiedAPI() string {
	s.T().Helper()
	return s.apiTrusting(loopbackTrusted, "")
}

// distributedAPI is the cell: the same trust, plus a declared content-delivery
// distribution in front of the whole deployment.
func (s *SecuritySuite) distributedAPI(trusted []string) string {
	s.T().Helper()
	return s.apiTrusting(trusted, clientaddr.CloudFrontViewerAddressHeader)
}

func (s *SecuritySuite) apiTrusting(trusted []string, edgeViewerHeader string) string {
	s.T().Helper()

	cfg := acceptconfig.DefaultConfig()
	cfg.Database.URL = os.Getenv("TEST_DATABASE_URL")
	if cfg.Database.URL == "" {
		cfg.Database.URL = "postgres://admin:secret@postgres:5432/golang-api?sslmode=disable"
	}
	cfg.RateLimit.TrustedProxies = trusted
	cfg.RateLimit.EdgeViewerHeader = edgeViewerHeader
	s.Require().False(cfg.RateLimit.Active(), "this application resolves addresses with the limiter OFF")

	app, err := bootstrap.New(cfg, types.ModeExternal)
	s.Require().NoError(err)
	server := httptest.NewServer(app.Router)

	s.T().Cleanup(func() {
		server.Close()
		s.Require().NoError(app.Close())
		_ = os.RemoveAll(cfg.Storage.FS.Root)
	})
	return server.URL
}

// loginAt fires one login at an address that exists nowhere, with whatever
// headers the case is about, and returns the status.
func (s *SecuritySuite) loginAt(baseURL, email string, headers map[string]string) int {
	s.T().Helper()
	return s.postAt(baseURL, "/auth/login",
		map[string]any{"email": email, "password": "whatever"}, headers)
}

// acceptInviteAt redeems (or fails to redeem) an invitation and returns the
// status.
func (s *SecuritySuite) acceptInviteAt(baseURL, token string, headers map[string]string) int {
	s.T().Helper()
	return s.postAt(baseURL, "/auth/accept-invite",
		map[string]any{"token": token, "password": "correct-horse-battery-staple"}, headers)
}

// postAt is the raw client these tests need: the suite's request builder sets no
// arbitrary headers, and the header is the whole point here.
func (s *SecuritySuite) postAt(baseURL, path string, body any, headers map[string]string) int {
	s.T().Helper()

	encoded, err := json.Marshal(body)
	s.Require().NoError(err)

	req, err := http.NewRequestWithContext(s.T().Context(), http.MethodPost, baseURL+path, bytes.NewReader(encoded))
	s.Require().NoError(err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	s.Require().NoError(err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}
