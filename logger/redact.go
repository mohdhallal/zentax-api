package logger

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

// Redaction at the emit boundary — ADR-0015.
//
// Logs sit OUTSIDE the erasure boundary (append-only, replicated, fanned into
// monitoring), so personal data that reaches a log line is effectively
// unerasable: no deletion request can reach it. The rule the ADR draws from
// that is structural — reference IDs only, never names, e-mail addresses, tax
// figures or free text — and it is enforced HERE, at the one place every line
// passes through, rather than by asking each call site to remember it.
//
// Three layers, each covering what the one before it cannot see:
//
//  1. RedactPath / RedactQuery / RedactText — what the HTTP boundary (the
//     request logger, the error handler) uses to shape a request into something
//     loggable. A query's KEYS survive, its values never do: that a search
//     happened is useful, the term searched for is not.
//  2. redactHandler — a slog.Handler in front of the real one. The value of any
//     attribute whose KEY names personal data or a secret is replaced before
//     emit, whatever the call site passed, including inside groups and through
//     LogValuer; and an e-mail-shaped run of text is scrubbed out of any string
//     value, wherever it came from (a driver error's "Key (email)=(…)" detail,
//     say). This is the layer that catches keys computed at runtime, which a
//     source-level check cannot.
//  3. logscan_test.go — the build gate. A call site that hands a logger the raw
//     request URI, a whole object, or a sensitive key fails the build, so the
//     fix cannot quietly rot back out.

// Redacted stands in for a value that was withheld. It is a fixed marker rather
// than an empty string so a reader can tell "withheld" from "absent".
const Redacted = "[redacted]"

// Bounds. A caller controls its own URL, headers and payload, so every one of
// these is attacker-chosen free text and none of it may be written unbounded
// into a log the operator pays to store.
const (
	maxQueryPairs   = 24
	maxKeyRunes     = 64
	maxPathSegments = 16
	maxSegmentRunes = 96

	// MaxTextRunes is the clip applied to a free-text value (an error string, a
	// User-Agent) that is logged despite not being a reference ID.
	MaxTextRunes = 512
)

// emailLike matches an e-mail-shaped run of text. It is deliberately loose:
// over-redacting a value that merely looks like an address costs a reader
// nothing, while missing a real one is permanent.
var emailLike = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

// RedactQuery renders a raw query string with every VALUE replaced and the keys
// kept: "search=jane%40acme.test&page=2" becomes "search=[redacted]&page=[redacted]".
// Keeping the keys is the point — "someone searched the member list" is an
// operational fact worth having, and it is the only part of a query that is not
// the caller's own data.
//
// A token with no "=" is dropped entirely: "?jane@acme.test" is a value wearing
// a key's clothing, and nothing distinguishes the two. A key that is not plainly
// a parameter name (anything outside [A-Za-z0-9_.-] and the [] of array
// notation, or longer than 64 runes) is redacted for the same reason.
func RedactQuery(raw string) string {
	raw = strings.TrimPrefix(raw, "?")
	if raw == "" {
		return ""
	}

	var b strings.Builder
	pairs := 0
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		if pairs >= maxQueryPairs {
			b.WriteString("&…")
			break
		}
		if pairs > 0 {
			b.WriteByte('&')
		}

		key, _, hasValue := strings.Cut(pair, "=")
		if !hasValue {
			b.WriteString(Redacted)
		} else {
			b.WriteString(safeKey(key))
			b.WriteByte('=')
			b.WriteString(Redacted)
		}
		pairs++
	}
	return b.String()
}

// RedactPath renders a request path segment by segment, keeping the segments
// that are shaped like a path literal or a reference ID and replacing the rest.
//
// On a matched route the segments are ours by construction — a literal from the
// router or an id — so the path survives intact and stays as useful as it was.
// The value of the pass is the unmatched request: a 404 for "/jane@acme.test"
// would otherwise write the caller's free text into the log verbatim.
//
// Any query or fragment is cut off; use RedactQuery for the query.
func RedactPath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		return "/"
	}

	segments := strings.Split(p, "/")
	out := make([]string, 0, len(segments))
	kept := 0
	for _, segment := range segments {
		if segment == "" {
			out = append(out, segment) // preserves the leading (and any trailing) slash
			continue
		}
		if kept >= maxPathSegments {
			out = append(out, "…")
			break
		}
		out = append(out, safeSegment(segment))
		kept++
	}
	return strings.Join(out, "/")
}

// RedactText prepares a free-text value for a log line: e-mail addresses out,
// length bounded. It is a mitigation, not a guarantee — free text can hold a
// name or a tax figure that no pattern will recognize — so it belongs on values
// that have nowhere else to go (an error string, a User-Agent), never as a
// licence to log a field that could have been a reference ID instead.
func RedactText(s string, maxRunes int) string {
	return clip(scrubEmails(s), maxRunes)
}

func scrubEmails(s string) string {
	if strings.IndexByte(s, '@') < 0 { // the common case, at no cost
		return s
	}
	return emailLike.ReplaceAllLiteralString(s, Redacted)
}

func clip(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if len(s) <= maxRunes { // bytes >= runes, so this settles most calls
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

func safeKey(key string) string {
	if key == "" {
		return Redacted
	}
	runes := 0
	for _, r := range key {
		runes++
		if runes > maxKeyRunes || !isKeyRune(r) {
			return Redacted
		}
	}
	return key
}

func safeSegment(segment string) string {
	runes := 0
	for _, r := range segment {
		runes++
		if runes > maxSegmentRunes || !isSegmentRune(r) {
			return Redacted
		}
	}
	return segment
}

func isKeyRune(r rune) bool {
	return r == '[' || r == ']' || isSegmentRune(r)
}

func isSegmentRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '_', r == '.':
		return true
	default:
		// Everything else — '@', '%', '+', ':', a space, any non-ASCII rune —
		// says this is not a path literal or an id, so it is not ours to log.
		return false
	}
}

// Sensitive keys. The value of an attribute whose key matches is never emitted.
//
// The list is ADR-0015's ("authorization headers, cookies, tokens, password,
// secret, email") widened to the identifiers the product actually handles:
// names, phone numbers, postal addresses, search terms, national/tax ids.
//
// Two match modes, because a short stem inside a longer word is a false
// positive waiting to happen ("otp" in "notplanned"): stems match as a
// substring of the normalized key, so "filename" and "userEmail" are both
// caught; exact keys match whole.
//
// Deliberately absent: "key" (documents log a storage object key, which is
// three UUIDs — ADR-0022), "peer" (the rate limiter logs the socket peer only
// when that peer is a trusted proxy, i.e. infrastructure, and it is the one
// thing that tells an operator the proxy is not appending) and "namespace" /
// "hostname", which are deployment facts. Redacting those would cost a real
// signal and protect nobody.
var sensitiveKeyStems = []string{
	"password", "passwd", "secret", "token", "authorization", "cookie",
	"apikey", "credential", "privatekey", "bearer",
	"email", "phone", "mobile", "search", "postcode", "zipcode",
	"addr", // addr / address / ipAddress / remoteAddr / postalAddress
	"firstname", "lastname", "fullname", "username", "filename", "displayname",
	"taxid", "vatnumber", "nationalid", "passport",
}

var sensitiveKeysExact = map[string]struct{}{
	"otp": {}, "totp": {}, "mfacode": {}, "pin": {},
	"ssn": {}, "dob": {}, "iban": {}, "bic": {},
	"name": {}, "mail": {}, "ip": {},
}

// IsSensitiveKey reports whether an attribute key names something that must
// never carry a value into a log line. It is exported because the build gate in
// logscan_test.go applies the same list to source: what the handler would
// redact at runtime should fail at compile time instead, where the author can
// see it.
func IsSensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	if normalized == "" {
		return false
	}
	if _, ok := sensitiveKeysExact[normalized]; ok {
		return true
	}
	for _, stem := range sensitiveKeyStems {
		if strings.Contains(normalized, stem) {
			return true
		}
	}
	return false
}

func normalizeKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range strings.ToLower(key) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// redactHandler is the last thing between an attribute and the log store.
type redactHandler struct{ inner slog.Handler }

func (h redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h redactHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

func (h redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		redacted = append(redacted, redactAttr(a))
	}
	return redactHandler{inner: h.inner.WithAttrs(redacted)}
}

func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{inner: h.inner.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve() // a LogValuer must be seen before it is judged

	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		inner := make([]slog.Attr, 0, len(group))
		for _, g := range group {
			inner = append(inner, redactAttr(g))
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(inner...)}
	}

	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	if a.Value.Kind() == slog.KindString {
		if scrubbed := scrubEmails(a.Value.String()); scrubbed != a.Value.String() {
			return slog.String(a.Key, scrubbed)
		}
	}
	return a
}
