package smtpmail

import (
	"context"
	"crypto/tls"
	"net"
	netmail "net/mail"
	netsmtp "net/smtp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/platform/mail"
)

func message() mail.Message {
	return mail.Message{
		Kind:    "deadline.reminder",
		From:    mail.Address{Name: "ZenTax", Email: "no-reply@zentax.test"},
		To:      mail.Address{Name: "Jane Doe", Email: "jane@acme.test"},
		Subject: "ZenTax deadlines for 2026-09-13",
		Text:    "Your VAT return is due on 2026-09-30.\n",
		HTML:    "<p>Your VAT return is due on 2026-09-30.</p>\n",
	}
}

// senderTo builds an adapter pointed at a fake relay: the options are the real
// ones an operator sets, and only the dial is redirected to the loopback
// listener so that Host stays a name TLS can verify.
func senderTo(t *testing.T, relay *fakeRelay, opts Options) *Sender {
	t.Helper()
	if opts.Host == "" {
		opts.Host = "localhost"
	}
	s, err := New(opts)
	require.NoError(t, err)
	s.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, relay.addr())
	}
	return s
}

// --- construction fails closed ----------------------------------------------

func TestNew_Defaults(t *testing.T) {
	t.Parallel()

	s, err := New(Options{Host: "relay.internal"})
	require.NoError(t, err)
	assert.Equal(t, DefaultPort, s.opts.Port)
	assert.Equal(t, TLSStartTLS, s.opts.TLS, "the default transport is upgraded, never plain")
	assert.Equal(t, AuthNone, s.opts.Auth, "no username means no authentication")
	assert.Equal(t, DefaultTimeout, s.opts.Timeout)
	assert.Equal(t, "localhost", s.opts.LocalName)

	s, err = New(Options{Host: "relay.internal", Username: "u", Password: "p"})
	require.NoError(t, err)
	assert.Equal(t, AuthPlain, s.opts.Auth, "a username is the operator asking to authenticate")
}

func TestNew_RefusesWhatWouldSendInTheClear(t *testing.T) {
	t.Parallel()

	_, err := New(Options{Host: "relay.internal", TLS: TLSNone})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "in the clear")

	_, err = New(Options{Host: "relay.internal", SkipTLSVerify: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate verification")

	// Both are allowed once the operator says so explicitly.
	_, err = New(Options{Host: "relay.internal", TLS: TLSNone, AllowInsecure: true})
	require.NoError(t, err)
	_, err = New(Options{Host: "relay.internal", SkipTLSVerify: true, AllowInsecure: true})
	require.NoError(t, err)
}

func TestNew_RefusesMalformedOptions(t *testing.T) {
	t.Parallel()

	for name, opts := range map[string]Options{
		"no host":             {},
		"port out of range":   {Host: "relay.internal", Port: 70000},
		"unknown auth":        {Host: "relay.internal", Auth: "oauth2", Username: "u", Password: "p"},
		"unknown tls":         {Host: "relay.internal", TLS: "ssl"},
		"auth without secret": {Host: "relay.internal", Auth: AuthPlain, Username: "u"},
		"missing ca bundle":   {Host: "relay.internal", CAFile: "/nonexistent/ca.pem"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := New(opts)
			require.Error(t, err)
		})
	}
}

// --- delivery ---------------------------------------------------------------

func TestSend_OverAnUnprotectedRelayTheOperatorAllowed(t *testing.T) {
	t.Parallel()

	relay := newRelay(t, nil)
	sender := senderTo(t, relay, Options{TLS: TLSNone, AllowInsecure: true, LocalName: "zentax.test"})

	require.NoError(t, sender.Send(context.Background(), message()))

	session := relay.lastSession(t)
	assert.Equal(t, "zentax.test", session.ehlo)
	assert.Equal(t, "FROM:<no-reply@zentax.test>", session.from)
	assert.Equal(t, []string{"TO:<jane@acme.test>"}, session.rcpt)
	assert.Empty(t, session.authArg, "no credentials without an auth method")

	// The DATA blob must be a parseable message carrying both alternatives.
	parsed, err := netmail.ReadMessage(strings.NewReader(session.data))
	require.NoError(t, err)
	assert.Equal(t, "ZenTax deadlines for 2026-09-13", parsed.Header.Get("Subject"))
	assert.Contains(t, parsed.Header.Get("Content-Type"), "multipart/alternative")
	assert.Contains(t, session.data, "text/html")
}

func TestSend_UpgradesWithSTARTTLSAndVerifiesTheOperatorsCA(t *testing.T) {
	t.Parallel()

	cert, caPEM := selfSignedCert(t)
	relay := newRelay(t, func(r *fakeRelay) {
		r.advertiseSTARTTLS = true
		r.tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	})

	sender := senderTo(t, relay, Options{
		TLS:      TLSStartTLS,
		CAFile:   writeCAFile(t, caPEM),
		Auth:     AuthPlain,
		Username: "zentax",
		Password: "s3cret",
	})

	require.NoError(t, sender.Send(context.Background(), message()))

	session := relay.lastSession(t)
	assert.True(t, session.overTLS, "the session must be upgraded before anything is sent")
	assert.True(t, strings.HasPrefix(session.authArg, "PLAIN "), "got %q", session.authArg)
	assert.Contains(t, session.data, "Subject:")
}

func TestSend_ImplicitTLS(t *testing.T) {
	t.Parallel()

	cert, caPEM := selfSignedCert(t)
	relay := newRelay(t, func(r *fakeRelay) {
		r.implicitTLS = true
		r.tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	})

	sender := senderTo(t, relay, Options{TLS: TLSImplicit, CAFile: writeCAFile(t, caPEM)})
	require.NoError(t, sender.Send(context.Background(), message()))
	assert.True(t, relay.lastSession(t).overTLS)
}

func TestSend_LoginAuthAgainstAnOlderConnector(t *testing.T) {
	t.Parallel()

	cert, caPEM := selfSignedCert(t)
	relay := newRelay(t, func(r *fakeRelay) {
		r.advertiseSTARTTLS = true
		r.tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	})

	sender := senderTo(t, relay, Options{
		TLS:      TLSStartTLS,
		CAFile:   writeCAFile(t, caPEM),
		Auth:     AuthLogin,
		Username: "zentax",
		Password: "s3cret",
	})
	require.NoError(t, sender.Send(context.Background(), message()))

	session := relay.lastSession(t)
	assert.Equal(t, "LOGIN", session.authArg)
	assert.Equal(t, "zentax", session.authUser)
	assert.Equal(t, "s3cret", session.authPass)
}

func TestSend_RefusesARelayThatDoesNotOfferSTARTTLS(t *testing.T) {
	t.Parallel()

	relay := newRelay(t, nil) // advertises no STARTTLS
	sender := senderTo(t, relay, Options{TLS: TLSStartTLS})

	err := sender.Send(context.Background(), message())
	require.Error(t, err)
	assert.True(t, mail.IsPermanent(err), "a relay that cannot be upgraded will not start tomorrow")
	assert.Contains(t, err.Error(), "STARTTLS")

	// With explicit consent the same relay is accepted.
	relaxed := senderTo(t, relay, Options{TLS: TLSStartTLS, AllowInsecure: true})
	require.NoError(t, relaxed.Send(context.Background(), message()))
}

func TestAuth_RefusesCredentialsOverAnUnencryptedSession(t *testing.T) {
	t.Parallel()

	// The adapter's own guard: without explicit consent, a password is never
	// offered on a session that was not upgraded.
	s, err := New(Options{Host: "relay.internal", Auth: AuthPlain, Username: "u", Password: "p"})
	require.NoError(t, err)

	_, err = s.auth(false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unencrypted")

	auth, err := s.auth(true)
	require.NoError(t, err)
	assert.NotNil(t, auth)
}

func TestSend_PlainAuthOverAPlaintextRelayIsStillRefused(t *testing.T) {
	t.Parallel()

	relay := newRelay(t, nil)
	sender := senderTo(t, relay, Options{
		Host: "relay.internal", // not localhost: net/smtp exempts only the loopback names
		TLS:  TLSNone, AllowInsecure: true,
		Auth: AuthPlain, Username: "u", Password: "p",
	})

	// AllowInsecure is consent for the transport; net/smtp's PlainAuth applies
	// its own rule on top and still refuses to hand a password to an
	// unencrypted, non-local server. Defence in depth, and the password never
	// reaches the wire.
	err := sender.Send(context.Background(), message())
	require.Error(t, err)
	assert.Empty(t, relay.lastSession(t).authPass, "no password reached the relay")
}

// --- classification ---------------------------------------------------------

func TestSend_5xxIsPermanentAnd4xxIsRetryable(t *testing.T) {
	t.Parallel()

	permanent := newRelay(t, func(r *fakeRelay) { r.rcptReply = "550 5.1.1 <jane@acme.test>: unknown user" })
	err := senderTo(t, permanent, Options{TLS: TLSNone, AllowInsecure: true}).Send(context.Background(), message())
	require.Error(t, err)
	assert.True(t, mail.IsPermanent(err), "a 5xx is the relay saying never")
	assert.Contains(t, err.Error(), "550")

	transient := newRelay(t, func(r *fakeRelay) { r.rcptReply = "451 4.3.0 greylisted, try later" })
	err = senderTo(t, transient, Options{TLS: TLSNone, AllowInsecure: true}).Send(context.Background(), message())
	require.Error(t, err)
	assert.True(t, mail.IsRetryable(err), "a 4xx is the relay saying not now")
	assert.Contains(t, err.Error(), "451")
}

func TestSend_UnreachableRelayIsRetryable(t *testing.T) {
	t.Parallel()

	// A listener that is closed immediately gives a port nothing is on.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().(*net.TCPAddr)
	require.NoError(t, listener.Close())

	sender, err := New(Options{Host: "127.0.0.1", Port: addr.Port, TLS: TLSNone, AllowInsecure: true})
	require.NoError(t, err)

	err = sender.Send(context.Background(), message())
	require.Error(t, err)
	assert.True(t, mail.IsRetryable(err), "an unreachable relay is the case the outbox exists for")
}

func TestSend_RejectsAMalformedMessageWithoutConnecting(t *testing.T) {
	t.Parallel()

	sender, err := New(Options{Host: "127.0.0.1", Port: 1, TLS: TLSNone, AllowInsecure: true})
	require.NoError(t, err)

	m := message()
	m.To = mail.Address{Email: "not-an-address"}
	err = sender.Send(context.Background(), m)
	require.Error(t, err)
	assert.True(t, mail.IsPermanent(err))
}

// --- LOGIN mechanism --------------------------------------------------------

func TestLoginAuth_RefusesAnUnencryptedSession(t *testing.T) {
	t.Parallel()

	a := &loginAuth{username: "u", password: "p", host: "relay.internal"}
	_, _, err := a.Start(&netsmtp.ServerInfo{Name: "relay.internal", TLS: false})
	require.Error(t, err)

	_, _, err = a.Start(&netsmtp.ServerInfo{Name: "relay.internal", TLS: true})
	require.NoError(t, err)
}

func TestLoginAuth_RefusesAnUnexpectedHost(t *testing.T) {
	t.Parallel()

	a := &loginAuth{username: "u", password: "p", host: "relay.internal", allowUnencrypted: true}
	_, _, err := a.Start(&netsmtp.ServerInfo{Name: "elsewhere.example", TLS: true})
	require.Error(t, err, "credentials go to the relay that was configured")
}

func TestLoginAuth_AnswersTheChallenges(t *testing.T) {
	t.Parallel()

	a := &loginAuth{username: "zentax", password: "s3cret", host: "relay.internal"}
	reply, err := a.Next([]byte("Username:"), true)
	require.NoError(t, err)
	assert.Equal(t, "zentax", string(reply))

	reply, err = a.Next([]byte("Password:"), true)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", string(reply))

	_, err = a.Next([]byte("Something else:"), true)
	require.Error(t, err)

	reply, err = a.Next(nil, false)
	require.NoError(t, err)
	assert.Nil(t, reply)
}

func TestSender_SatisfiesTheSeam(t *testing.T) {
	t.Parallel()
	s, err := New(Options{Host: "relay.internal"})
	require.NoError(t, err)
	var seam mail.Sender = s
	assert.NotNil(t, seam)
}
