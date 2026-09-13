package smtpmail

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A fake relay on a loopback listener. It speaks enough SMTP to exercise the
// adapter's real code path — EHLO, STARTTLS, AUTH, MAIL/RCPT/DATA — so the
// tests assert on a protocol conversation rather than on a mocked client, and
// still touch no network beyond 127.0.0.1.

type relaySession struct {
	ehlo     string
	authArg  string
	authUser string
	authPass string
	from     string
	rcpt     []string
	data     string
	overTLS  bool
}

type fakeRelay struct {
	listener net.Listener
	tlsConf  *tls.Config

	// knobs
	advertiseSTARTTLS bool
	implicitTLS       bool
	rcptReply         string // default "250 2.1.5 Ok"
	authReply         string // default "235 2.7.0 Authentication successful"

	mu       sync.Mutex
	sessions []relaySession
	wg       sync.WaitGroup
}

func newRelay(t *testing.T, configure func(*fakeRelay)) *fakeRelay {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	r := &fakeRelay{
		listener:  listener,
		rcptReply: "250 2.1.5 Ok",
		authReply: "235 2.7.0 Authentication successful",
	}
	if configure != nil {
		configure(r)
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			r.handle(conn)
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close()
		r.wg.Wait()
	})
	return r
}

func (r *fakeRelay) addr() string { return r.listener.Addr().String() }

func (r *fakeRelay) lastSession(t *testing.T) relaySession {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.sessions, "the relay saw no session")
	return r.sessions[len(r.sessions)-1]
}

func (r *fakeRelay) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	var s relaySession
	recorded := false
	record := func() {
		if recorded {
			return
		}
		recorded = true
		r.mu.Lock()
		r.sessions = append(r.sessions, s)
		r.mu.Unlock()
	}
	defer record()

	if r.implicitTLS {
		tlsConn := tls.Server(conn, r.tlsConf)
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		conn = tlsConn
		s.overTLS = true
	}

	text := textproto.NewConn(conn)
	_ = text.PrintfLine("220 fake.relay ESMTP")

	for {
		line, err := text.ReadLine()
		if err != nil {
			return
		}
		cmd, arg, _ := strings.Cut(line, " ")

		switch strings.ToUpper(cmd) {
		case "EHLO", "HELO":
			s.ehlo = arg
			caps := []string{"fake.relay greets you"}
			if r.advertiseSTARTTLS && !s.overTLS {
				caps = append(caps, "STARTTLS")
			}
			caps = append(caps, "AUTH PLAIN LOGIN CRAM-MD5")
			for i, c := range caps {
				sep := "-"
				if i == len(caps)-1 {
					sep = " "
				}
				_ = text.PrintfLine("250%s%s", sep, c)
			}

		case "STARTTLS":
			_ = text.PrintfLine("220 2.0.0 Ready to start TLS")
			tlsConn := tls.Server(conn, r.tlsConf)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			text = textproto.NewConn(conn)
			s.overTLS = true

		case "AUTH":
			s.authArg = arg
			if strings.HasPrefix(strings.ToUpper(arg), "LOGIN") {
				_ = text.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte("Username:")))
				user, err := text.ReadLine()
				if err != nil {
					return
				}
				_ = text.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte("Password:")))
				pass, err := text.ReadLine()
				if err != nil {
					return
				}
				s.authUser, s.authPass = decodeB64(user), decodeB64(pass)
			}
			_ = text.PrintfLine("%s", r.authReply)

		case "MAIL":
			s.from = arg
			_ = text.PrintfLine("250 2.1.0 Ok")

		case "RCPT":
			s.rcpt = append(s.rcpt, arg)
			_ = text.PrintfLine("%s", r.rcptReply)

		case "DATA":
			_ = text.PrintfLine("354 End data with <CR><LF>.<CR><LF>")
			body, err := text.ReadDotBytes()
			if err != nil {
				return
			}
			s.data = string(body)
			_ = text.PrintfLine("250 2.0.0 Ok: queued as FAKE1")

		case "QUIT":
			_ = text.PrintfLine("221 2.0.0 Bye")
			record()
			return

		case "NOOP", "RSET":
			_ = text.PrintfLine("250 2.0.0 Ok")

		default:
			_ = text.PrintfLine("500 5.5.2 Unrecognised command")
		}
	}
}

func decodeB64(s string) string {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	return string(raw)
}

// selfSignedCert mints a certificate for "localhost" and returns it with the
// PEM an operator would put in mail.smtp.caFile — which is how the TLS tests
// keep verification ON rather than testing a skipped check.
func selfSignedCert(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	return cert, certPEM
}

func writeCAFile(t *testing.T, pemBytes []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relay-ca.pem")
	require.NoError(t, os.WriteFile(path, pemBytes, 0o600))
	return path
}
