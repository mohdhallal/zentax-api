package securityevent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// The subject digest: how the stream correlates attempts against an address it
// must not keep.
//
// THE PROBLEM. The most valuable question a failed-login trail answers is "how
// many attempts hit THIS address, and from where" — and the address that makes
// that question askable is, when it resolves to no account, the personal data of
// somebody who is not a customer, in a store that is append-only and therefore
// outside the erasure boundary. Keeping it is not defensible; losing the ability
// to count is not either.
//
// WHY KEYED, AND NOT A PLAIN HASH. The space of e-mail addresses is small and
// public. An unkeyed SHA-256 of an address is a lookup away from the address, so
// it is a pseudonym only against somebody who has not thought about it; a leaked
// table of unkeyed digests is a leaked table of addresses. HMAC under a key that
// lives OUTSIDE the database (AUTH_ENCRYPTION_KEY, ADR-0006's interim single
// key) makes the digest testable only by whoever already holds the key — an
// investigator can digest a candidate address and select on it, and a copy of
// the table alone yields nothing.
//
// DOMAIN SEPARATION. The key used here is derived from the master key with a
// label rather than being the master key, so the digest key is not the key that
// opens TOTP seeds or outbox payloads: possession of one does not confer the
// other, and rotating this use later does not have to mean rotating that one.
//
// STABILITY IS THE FEATURE. The digest of one address must be the same digest
// tomorrow, or counting is impossible — so the input is normalised exactly the
// way the login path normalises an address before looking it up (trim, then
// lowercase), and the label carries a version so a future change to either half
// is a new namespace rather than a silent break in correlation.
type Digest struct{ key []byte }

// digestLabel names this use of the master key. Bump the version if the input
// normalisation or the construction ever changes: digests either correlate or
// they do not, and a half-changed one silently splits an address in two.
const digestLabel = "zentax:security-event:subject-digest:v1"

// keyLen is the master key's length (AES-256, as config.DecodeEncryptionKey
// produces). Checked here so a misconfigured key is a boot-time complaint
// rather than a per-event surprise.
const keyLen = 32

// NewDigest derives the subject-digest key from the master key.
func NewDigest(masterKey []byte) (*Digest, error) {
	if len(masterKey) != keyLen {
		return nil, fmt.Errorf(
			"securityevent: the subject digest needs a %d-byte key, got %d: %w",
			keyLen, len(masterKey), crypto.ErrInvalidKey)
	}
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte(digestLabel))
	return &Digest{key: mac.Sum(nil)}, nil
}

// DigestFromKey is the composition root's convenience: a usable master key
// gives a digest, an unusable one gives nil and a warning.
//
// nil is a real deployment and not only a test fixture — a development box with
// no encryption key configured, which is also the box where MFA is switched off
// and outbox payloads are stored in cleartext. What it costs is the correlator
// on unidentified attempts: the events are still recorded, they simply cannot be
// grouped by address. A deployed tier cannot reach that state (config
// .validateDeployed refuses to boot without a real key).
func DigestFromKey(masterKey []byte) *Digest {
	d, err := NewDigest(masterKey)
	if err != nil {
		logger.Log.Warn(
			"Security events will carry no subject correlator: no usable encryption key",
			logger.String("consequence", "failed logins for unknown addresses cannot be counted per address"),
			logger.String("remedy", "set AUTH_ENCRYPTION_KEY (base64 of 32 bytes, or a passphrase)"),
		)
		return nil
	}
	return d
}

// Of returns the hex digest of a submitted address, or "" when there is no key
// or no subject. A nil *Digest is a valid no-op.
func (d *Digest) Of(subject string) string {
	if d == nil {
		return ""
	}
	normalized := normalizeSubject(subject)
	if normalized == "" {
		return ""
	}
	mac := hmac.New(sha256.New, d.key)
	mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))
}

// normalizeSubject must agree with how the login path resolves an address to an
// account (modules/identity/usecases.Login): trim, then lowercase. If these two
// ever disagree, "  Admin@Acme.com " and "admin@acme.com" become two different
// subjects in the stream while being one account in the product — and the count
// an investigator reads is wrong in the direction that hides an attack.
func normalizeSubject(subject string) string {
	return strings.ToLower(strings.TrimSpace(subject))
}
