package outbox

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

// The payload at rest.
//
// WHY THE COLUMN CANNOT BE CLEARTEXT. An outbox row is a message the product
// owes a human, frozen until a runner can send it — and for one template that
// message CONTAINS A LIVE CREDENTIAL. member.invited carries the cleartext
// "zti_…" invite token, which is exactly the value invite_tokens refuses to
// keep: that table stores only a SHA-256, so a leaked table cannot be replayed.
// A cleartext payload defeats that control for the whole validity of the token
// — a database backup, a read-replica, a support query, a snapshot restored
// into a laptop all yield a working activation link for somebody else's tenant.
// The digest template is milder but the same shape: entity names, task names
// and due dates, i.e. a customer's compliance calendar.
//
// WHAT THIS DOES. The variable set — and only the variable set — is sealed with
// AES-256-GCM under the key the product already uses for a secret at rest
// (platform/crypto, ADR-0006's interim single key, the same one that protects
// totp_secret_enc). The cleartext exists in the database never, and in memory
// only between the claim and the transport call. Everything an operator needs
// to diagnose a stuck message stays in the clear alongside it: the template,
// the recipient, the schedule, the attempt count, the last error, the audit
// entry a dead-lettered message leaves.
//
// WHAT BOUNDS IT FURTHER. Sealing shrinks who can read the payload; clearing it
// at settlement shrinks how long it exists at all (see Store.MarkSent /
// settleDead, which set it back to '{}' on the same statement that settles the
// row). Together the window is "until delivery", not "until the token expires"
// and certainly not "until the 30-day pruner gets to it".
//
// WHY NOT ENCRYPT THE WHOLE ROW. A queue you cannot read is a queue you cannot
// operate. The recipient is personal data and is handled by the erasure path
// (the pruner), not by a cipher; the template and the timestamps are the only
// things that make an undelivered message diagnosable at all.
//
// FORWARD AND BACKWARD COMPATIBILITY. Open() passes a payload that is not an
// envelope through untouched, so rows queued before this change still deliver,
// and a build with no key configured still works — it just stores cleartext,
// which SealFromKey says out loud. A deployed tier cannot reach that state:
// config.validateDeployed refuses to boot without a real encryption key.
type Seal struct {
	key []byte
}

// sealVersion is the envelope's format version. It exists so a future key
// rotation or cipher change can be told apart from today's rows at read time
// rather than guessed at.
const sealVersion = 1

// keyLen is AES-256. crypto.Encrypt enforces it too; checking here means a
// misconfigured key is a boot-time complaint rather than a per-message failure.
const keyLen = 32

// envelope is what the payload column holds for a sealed message:
//
//	{"encV":1,"enc":"<base64 of nonce||ciphertext>"}
//
// It is still JSONB, so the column type, the state-only trigger and every
// existing index are unchanged; and the two keys are ones no template's
// variable set uses, so IsSealed can tell an envelope from a payload without a
// schema change.
type envelope struct {
	EncV int    `json:"encV"`
	Enc  string `json:"enc"`
}

// NewSeal builds the payload seal from a 32-byte AES-256 key.
func NewSeal(key []byte) (*Seal, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("outbox: payload seal needs a %d-byte key, got %d: %w", keyLen, len(key), crypto.ErrInvalidKey)
	}
	// Copied, so a caller reusing its buffer cannot change the key under a
	// running process — the kind of bug whose symptom is an unopenable queue.
	owned := make([]byte, keyLen)
	copy(owned, key)
	return &Seal{key: owned}, nil
}

// SealFromKey is the composition root's convenience: a usable key gives a seal,
// an unusable one gives nil and a warning.
//
// nil is the UNSEALED build — cleartext payloads, i.e. what the queue did
// before this existed. It is reachable only where the encryption key is already
// absent, which is also where MFA is already switched off (bootstrap builds the
// identity use cases with the same nil key), and never in a deployed tier:
// config.validateDeployed refuses to boot one without a real key. Warning
// rather than failing keeps a keyless development box able to invite somebody,
// and says in the log what that costs.
func SealFromKey(key []byte) *Seal {
	seal, err := NewSeal(key)
	if err != nil {
		logger.Log.Warn("Outbox payloads will be stored in cleartext: no usable encryption key",
			logger.String("remedy", "set AUTH_ENCRYPTION_KEY (base64 of 32 bytes, or a passphrase)"),
		)
		return nil
	}
	return seal
}

// Seal wraps a payload for storage. A nil *Seal stores it as it stands.
func (s *Seal) Seal(payload []byte) (json.RawMessage, error) {
	if s == nil {
		return payload, nil
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	ciphertext, err := crypto.Encrypt(s.key, payload)
	if err != nil {
		return nil, fmt.Errorf("outbox: seal payload: %w", err)
	}
	sealed, err := json.Marshal(envelope{EncV: sealVersion, Enc: ciphertext})
	if err != nil {
		return nil, fmt.Errorf("outbox: encode sealed payload: %w", err)
	}
	return sealed, nil
}

// Open returns the cleartext variable set of a stored payload.
//
// A payload that is not an envelope is returned untouched — that is what makes
// the change safe to deploy over a queue with rows already in it, and what
// keeps a settled row's cleared '{}' readable. An envelope with no key, an
// unknown version, or a ciphertext this key cannot open is an error, and the
// delivery loop turns it into a dead letter rather than eight pointless
// retries: none of those get better by waiting.
func (s *Seal) Open(payload []byte) (json.RawMessage, error) {
	env, sealed := parseEnvelope(payload)
	if !sealed {
		return payload, nil
	}
	if s == nil {
		return nil, errors.New("outbox: payload is sealed but no encryption key is configured")
	}
	if env.EncV != sealVersion {
		return nil, fmt.Errorf("outbox: sealed payload is version %d, this build reads %d", env.EncV, sealVersion)
	}
	plain, err := crypto.Decrypt(s.key, env.Enc)
	if err != nil {
		return nil, fmt.Errorf("outbox: open sealed payload: %w", err)
	}
	return plain, nil
}

// IsSealed reports whether a stored payload is an envelope rather than a
// cleartext variable set. It is exported so a test — including the acceptance
// suite, which reads the column as the application role does — can assert what
// is actually at rest.
func IsSealed(payload []byte) bool {
	_, sealed := parseEnvelope(payload)
	return sealed
}

func parseEnvelope(payload []byte) (envelope, bool) {
	if len(payload) == 0 {
		return envelope{}, false
	}
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return envelope{}, false
	}
	return env, env.Enc != ""
}
