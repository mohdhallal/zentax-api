package mail

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"strings"
	"time"
)

// BuildMIME renders a Message as an RFC 5322 message: headers, then a
// text/plain body, or a multipart/alternative body when HTML is present.
//
// It lives in the seam rather than in the SMTP adapter because it is pure,
// testable and shared: SMTP writes these bytes down a DATA command, and a
// future provider that only accepts a raw message (SES SendRawEmail, an
// on-prem relay) takes exactly the same output. The cloud adapter that speaks
// a structured API (sesmail) does not use it at all.
//
// Bodies are quoted-printable so that UTF-8, long lines and trailing
// whitespace all survive a 7-bit transport unchanged.
func BuildMIME(msg Message) ([]byte, error) {
	id, err := generateMessageID(msg.From.Domain())
	if err != nil {
		return nil, err
	}
	boundary, err := randomToken()
	if err != nil {
		return nil, err
	}
	return buildMIME(msg, mimeOptions{Now: time.Now(), MessageID: id, Boundary: boundary})
}

// mimeOptions are the three values that would otherwise make the output
// unreproducible. Tests pass them in; BuildMIME generates them.
type mimeOptions struct {
	Now       time.Time
	MessageID string
	Boundary  string
}

const crlf = "\r\n"

func buildMIME(msg Message, opt mimeOptions) ([]byte, error) {
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	if strings.Contains(msg.HTML, "--"+opt.Boundary) || strings.Contains(msg.Text, "--"+opt.Boundary) {
		return nil, fmt.Errorf("mail: body contains the multipart boundary")
	}

	var b strings.Builder
	writeHeader(&b, "From", msg.From.String())
	writeHeader(&b, "To", msg.To.String())
	if !msg.ReplyTo.IsZero() {
		writeHeader(&b, "Reply-To", msg.ReplyTo.String())
	}
	writeHeader(&b, "Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	writeHeader(&b, "Date", opt.Now.UTC().Format(time.RFC1123Z))
	writeHeader(&b, "Message-ID", opt.MessageID)
	// RFC 3834: this message is machine-generated. Without it a recipient's
	// out-of-office reply comes back to a no-reply mailbox, and some MTAs treat
	// the resulting loop as a reputation problem.
	writeHeader(&b, "Auto-Submitted", "auto-generated")
	writeHeader(&b, "MIME-Version", "1.0")

	if msg.HTML == "" {
		writeHeader(&b, "Content-Type", "text/plain; charset=UTF-8")
		writeHeader(&b, "Content-Transfer-Encoding", "quoted-printable")
		b.WriteString(crlf)
		if err := writeQuotedPrintable(&b, msg.Text); err != nil {
			return nil, err
		}
		return []byte(b.String()), nil
	}

	writeHeader(&b, "Content-Type", `multipart/alternative; boundary="`+opt.Boundary+`"`)
	b.WriteString(crlf)

	// Order matters: least-capable part first, so a client that understands
	// only one of them shows the plain text (RFC 2046 §5.1.4).
	for _, part := range []struct{ contentType, body string }{
		{"text/plain; charset=UTF-8", msg.Text},
		{"text/html; charset=UTF-8", msg.HTML},
	} {
		b.WriteString("--" + opt.Boundary + crlf)
		writeHeader(&b, "Content-Type", part.contentType)
		writeHeader(&b, "Content-Transfer-Encoding", "quoted-printable")
		b.WriteString(crlf)
		if err := writeQuotedPrintable(&b, part.body); err != nil {
			return nil, err
		}
		b.WriteString(crlf)
	}
	b.WriteString("--" + opt.Boundary + "--" + crlf)

	return []byte(b.String()), nil
}

// writeHeader writes one header, folded so no line runs past the 78-character
// soft limit.
//
// Two details keep folding invisible to the reader. It happens only at an
// existing space, so an encoded-word is never split down the middle; and the
// continuation begins with a SPACE rather than a tab, so it makes no difference
// whether the receiving parser unfolds by dropping the CRLF (leaving the space)
// or by joining on a single space — a display name comes back the way it went
// in either way.
func writeHeader(b *strings.Builder, name, value string) {
	const softLimit = 78

	line := name + ": "
	first := true
	for _, word := range strings.Split(value, " ") {
		switch {
		case first:
			line += word
			first = false
		case len(line)+1+len(word) <= softLimit:
			line += " " + word
		default:
			b.WriteString(line + crlf)
			line = " " + word
		}
	}
	b.WriteString(line + crlf)
}

func writeQuotedPrintable(b *strings.Builder, body string) error {
	// A bare CR would become a spurious line break; normalise to LF and let the
	// encoder emit the CRLF pairs the wire wants.
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	w := quotedprintable.NewWriter(b)
	if _, err := w.Write([]byte(body)); err != nil {
		return fmt.Errorf("mail: encode body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: encode body: %w", err)
	}
	return nil
}

// generateMessageID builds `<random@domain>` from the sender's domain, which is
// the domain that owns the message and the one a receiving MTA expects.
func generateMessageID(domain string) (string, error) {
	if domain == "" {
		return "", fmt.Errorf("mail: sender has no domain, cannot build a Message-ID")
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	return "<" + token + "@" + domain + ">", nil
}

func randomToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("mail: read random: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
