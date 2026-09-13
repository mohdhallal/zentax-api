// Package smtpmail is the SMTP adapter of platform/mail — the self-hosted
// edition's way out (ADR-0009: "Email — SES / SMTP"). It speaks to whatever
// relay the operator already runs: a corporate Exchange connector, Postfix on
// the same host, or a provider's SMTP endpoint.
//
// Two properties the rest of the seam depends on:
//
//   - It refuses to send in the clear. STARTTLS is the default, an implicit-TLS
//     port is supported, and a plaintext session — or one that skips
//     certificate verification — happens only when the operator has explicitly
//     said so (AllowInsecure). Mail carries invite tokens and filing data; a
//     silent downgrade to plaintext is a disclosure nobody would notice.
//   - Every failure is classified. A 5xx reply is the relay saying "never":
//     permanent, the outbox records it. Everything else — a refused connection,
//     a 4xx greylist, a TLS handshake that failed mid-renewal — is temporary
//     and worth another attempt.
//
// One connection per message. The scheduler delivers a handful of messages per
// tick, so a pooled connection would buy nothing and cost a class of bug (a
// half-open relay session held across ticks) that is expensive to find.
package smtpmail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	netsmtp "net/smtp"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/platform/mail"
)

// AuthMethod names how the adapter authenticates to the relay.
type AuthMethod string

const (
	// AuthNone is an open relay on a trusted network (a Postfix on localhost,
	// an internal smarthost that authorises by source address).
	AuthNone AuthMethod = "none"
	// AuthPlain is AUTH PLAIN — what nearly every provider wants.
	AuthPlain AuthMethod = "plain"
	// AuthLogin is AUTH LOGIN, which older Exchange connectors require.
	AuthLogin AuthMethod = "login"
	// AuthCRAMMD5 is AUTH CRAM-MD5: the password never crosses the wire, but
	// the relay must store it recoverably. Offered because some appliances
	// support nothing else.
	AuthCRAMMD5 AuthMethod = "crammd5"
)

// TLSMode names how the connection is protected.
type TLSMode string

const (
	// TLSStartTLS connects in the clear and upgrades (submission, port 587).
	TLSStartTLS TLSMode = "starttls"
	// TLSImplicit wraps the connection in TLS from the first byte (port 465).
	TLSImplicit TLSMode = "implicit"
	// TLSNone sends in the clear. Requires AllowInsecure.
	TLSNone TLSMode = "none"
)

// Defaults for an operator who names only a host.
const (
	DefaultPort    = 587
	DefaultTimeout = 15 * time.Second
)

// Options configures the adapter. Only Host has no usable default.
type Options struct {
	Host string
	Port int

	Username string
	Password string
	Auth     AuthMethod
	TLS      TLSMode

	// AllowInsecure is the operator's explicit consent to a session that is not
	// protected: TLS "none", or a certificate that is not verified. Without it
	// both are refused at construction, not at send time, so the mistake shows
	// up as a failed boot rather than as mail that quietly went out in plain
	// text for a month.
	AllowInsecure bool
	// SkipTLSVerify accepts any certificate. Requires AllowInsecure.
	SkipTLSVerify bool
	// CAFile is a PEM bundle to verify the relay against — the ordinary answer
	// for an internal MTA with a private CA, and the one that keeps
	// verification on.
	CAFile string

	// Timeout bounds the whole session when the context carries no deadline.
	Timeout time.Duration
	// LocalName is the EHLO name. Relays that check it want a real hostname.
	LocalName string
}

// Sender delivers through an SMTP relay.
type Sender struct {
	opts  Options
	roots *x509.CertPool
	// dial is a seam: tests point it at a loopback listener.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

var _ mail.Sender = (*Sender)(nil)

// New validates the options and builds the adapter. It fails closed: an
// unknown auth method or TLS mode, a missing host, credentials without a user,
// or an insecure transport without explicit consent are all refused here.
func New(opts Options) (*Sender, error) {
	if strings.TrimSpace(opts.Host) == "" {
		return nil, errors.New("smtp mail: host is required")
	}
	if opts.Port == 0 {
		opts.Port = DefaultPort
	}
	if opts.Port < 1 || opts.Port > 65535 {
		return nil, fmt.Errorf("smtp mail: port %d is out of range", opts.Port)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.LocalName == "" {
		opts.LocalName = "localhost"
	}
	if opts.Auth == "" {
		// A username is the operator saying "authenticate"; without one the
		// only coherent default is an unauthenticated relay.
		if opts.Username != "" {
			opts.Auth = AuthPlain
		} else {
			opts.Auth = AuthNone
		}
	}
	if opts.TLS == "" {
		opts.TLS = TLSStartTLS
	}

	switch opts.Auth {
	case AuthNone:
	case AuthPlain, AuthLogin, AuthCRAMMD5:
		if opts.Username == "" || opts.Password == "" {
			return nil, fmt.Errorf("smtp mail: auth %q needs a username and a password", opts.Auth)
		}
	default:
		return nil, fmt.Errorf("smtp mail: auth must be %q, %q, %q or %q, got %q",
			AuthNone, AuthPlain, AuthLogin, AuthCRAMMD5, opts.Auth)
	}

	switch opts.TLS {
	case TLSStartTLS, TLSImplicit:
	case TLSNone:
		if !opts.AllowInsecure {
			return nil, errors.New("smtp mail: refusing to send in the clear — set tls to starttls/implicit, or allowInsecure to accept an unprotected relay")
		}
	default:
		return nil, fmt.Errorf("smtp mail: tls must be %q, %q or %q, got %q", TLSStartTLS, TLSImplicit, TLSNone, opts.TLS)
	}
	if opts.SkipTLSVerify && !opts.AllowInsecure {
		return nil, errors.New("smtp mail: refusing to skip certificate verification — set allowInsecure to accept it, or point caFile at the relay's CA")
	}

	s := &Sender{opts: opts, dial: dialContext}
	if opts.CAFile != "" {
		pool, err := loadRoots(opts.CAFile)
		if err != nil {
			return nil, err
		}
		s.roots = pool
	}
	return s, nil
}

func dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

func loadRoots(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path) //nolint:gosec // an operator-configured CA bundle path, read at startup
	if err != nil {
		return nil, fmt.Errorf("smtp mail: read caFile: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("smtp mail: caFile %q contains no certificate", path)
	}
	return pool, nil
}

// Send delivers one message.
func (s *Sender) Send(ctx context.Context, msg mail.Message) error {
	if err := msg.Validate(); err != nil {
		return mail.Permanent("smtp: message", "", err)
	}
	raw, err := mail.BuildMIME(msg)
	if err != nil {
		return mail.Permanent("smtp: render", "", err)
	}

	addr := net.JoinHostPort(s.opts.Host, strconv.Itoa(s.opts.Port))
	conn, err := s.dial(ctx, "tcp", addr)
	if err != nil {
		return mail.Retryable("smtp: dial", "", err)
	}
	defer func() { _ = conn.Close() }()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(s.opts.Timeout)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return mail.Retryable("smtp: deadline", "", err)
	}

	if s.opts.TLS == TLSImplicit {
		tlsConn := tls.Client(conn, s.tlsConfig())
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return mail.Retryable("smtp: tls handshake", "", err)
		}
		conn = tlsConn
	}

	client, err := netsmtp.NewClient(conn, s.opts.Host)
	if err != nil {
		return classify("smtp: greeting", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Hello(s.opts.LocalName); err != nil {
		return classify("smtp: ehlo", err)
	}

	encrypted := s.opts.TLS == TLSImplicit
	if s.opts.TLS == TLSStartTLS {
		supported, _ := client.Extension("STARTTLS")
		switch {
		case supported:
			if err := client.StartTLS(s.tlsConfig()); err != nil {
				return classify("smtp: starttls", err)
			}
			encrypted = true
		case s.opts.AllowInsecure:
			// The operator accepted an unprotected relay; a server that does
			// not offer STARTTLS is then a fact, not a failure.
		default:
			return mail.Permanent("smtp: starttls", "", errors.New("the relay does not offer STARTTLS (set allowInsecure to send anyway)"))
		}
	}

	auth, err := s.auth(encrypted)
	if err != nil {
		return mail.Permanent("smtp: auth", "", err)
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return classify("smtp: auth", err)
		}
	}

	if err := client.Mail(msg.From.Email); err != nil {
		return classify("smtp: mail from", err)
	}
	if err := client.Rcpt(msg.To.Email); err != nil {
		return classify("smtp: rcpt to", err)
	}
	w, err := client.Data()
	if err != nil {
		return classify("smtp: data", err)
	}
	if _, err := w.Write(raw); err != nil {
		return classify("smtp: write", err)
	}
	if err := w.Close(); err != nil {
		return classify("smtp: write", err)
	}
	// The relay accepted the message at the end of DATA. A QUIT that then fails
	// is a closed socket, not an undelivered message, and retrying would
	// deliver it twice.
	_ = client.Quit()
	return nil
}

// tlsConfig is TLS 1.2 or better, verified against the system roots or the
// operator's CA bundle. Verification is skipped only with explicit consent,
// which New has already checked.
func (s *Sender) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName:         s.opts.Host,
		MinVersion:         tls.VersionTLS12,
		RootCAs:            s.roots,
		InsecureSkipVerify: s.opts.SkipTLSVerify, //nolint:gosec // G402: gated on Options.AllowInsecure at construction; an internal MTA with a private CA is the case that needs it
	}
}

// auth builds the SASL mechanism, refusing to hand credentials to an
// unencrypted session unless the operator allowed it. net/smtp's PlainAuth
// enforces the same rule for PLAIN; loginAuth enforces it for LOGIN, which is
// the mechanism most likely to be paired with an old plaintext connector.
func (s *Sender) auth(encrypted bool) (netsmtp.Auth, error) {
	if s.opts.Auth == AuthNone {
		return nil, nil //nolint:nilnil // "no mechanism, no error" is the whole meaning of AuthNone
	}
	if !encrypted && !s.opts.AllowInsecure {
		return nil, errors.New("refusing to send credentials over an unencrypted connection")
	}
	switch s.opts.Auth {
	case AuthPlain:
		return netsmtp.PlainAuth("", s.opts.Username, s.opts.Password, s.opts.Host), nil
	case AuthLogin:
		return &loginAuth{username: s.opts.Username, password: s.opts.Password, host: s.opts.Host, allowUnencrypted: s.opts.AllowInsecure}, nil
	case AuthCRAMMD5:
		return netsmtp.CRAMMD5Auth(s.opts.Username, s.opts.Password), nil
	default:
		return nil, fmt.Errorf("unknown auth method %q", s.opts.Auth)
	}
}

// classify turns a relay's answer into the verdict the outbox acts on: 5xx is
// the relay refusing for good, everything else is worth another attempt.
func classify(op string, err error) error {
	var proto *textproto.Error
	if errors.As(err, &proto) {
		code := strconv.Itoa(proto.Code)
		if proto.Code >= 500 && proto.Code < 600 {
			return mail.Permanent(op, code, err)
		}
		return mail.Retryable(op, code, err)
	}
	return mail.Retryable(op, "", err)
}

// loginAuth implements AUTH LOGIN, which net/smtp does not ship: the server
// prompts for the username and then the password, each base64-encoded (the
// client decodes them before Next sees them).
type loginAuth struct {
	username, password string
	host               string
	allowUnencrypted   bool
}

func (a *loginAuth) Start(server *netsmtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !a.allowUnencrypted {
		return "", nil, errors.New("smtp mail: refusing LOGIN credentials over an unencrypted connection")
	}
	// The same host check net/smtp's PlainAuth makes: credentials go to the
	// relay that was configured, never to one a redirect moved us to.
	if server.Name != a.host {
		return "", nil, fmt.Errorf("smtp mail: relay identified itself as %q, expected %q", server.Name, a.host)
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(strings.TrimSuffix(string(fromServer), ":"))) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("smtp mail: unexpected LOGIN challenge %q", string(fromServer))
	}
}
