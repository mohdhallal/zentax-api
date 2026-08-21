// Package crypto provides the auth primitives: argon2id password hashing,
// AES-GCM secret encryption (interim until ADR-0006 per-tenant keys), and
// session-token generation/hashing.
package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrInvalidHash is returned when an encoded password hash cannot be parsed.
var ErrInvalidHash = errors.New("crypto: invalid password hash format")

type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLength  uint32
	keyLength   uint32
}

// OWASP-aligned argon2id defaults.
var defaultParams = argon2Params{
	memory:      64 * 1024, // 64 MiB
	iterations:  3,
	parallelism: 2,
	saltLength:  16,
	keyLength:   32,
}

// HashPassword returns a self-describing argon2id encoded hash
// ($argon2id$v=19$m=..,t=..,p=..$salt$hash).
func HashPassword(password string) (string, error) {
	salt := make([]byte, defaultParams.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("crypto: read salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt,
		defaultParams.iterations, defaultParams.memory, defaultParams.parallelism, defaultParams.keyLength)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, defaultParams.memory, defaultParams.iterations, defaultParams.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword reports whether password matches the encoded argon2id hash,
// using a constant-time comparison.
func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, wantHash, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	gotHash := argon2.IDKey([]byte(password), salt,
		params.iterations, params.memory, params.parallelism, params.keyLength)
	return subtle.ConstantTimeCompare(gotHash, wantHash) == 1, nil
}

func decodeHash(encoded string) (argon2Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argon2Params{}, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return argon2Params{}, nil, nil, ErrInvalidHash
	}

	var p argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &p.parallelism); err != nil {
		return argon2Params{}, nil, nil, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2Params{}, nil, nil, ErrInvalidHash
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2Params{}, nil, nil, ErrInvalidHash
	}
	p.saltLength = uint32(len(salt))
	p.keyLength = uint32(len(hash))
	return p, salt, hash, nil
}
