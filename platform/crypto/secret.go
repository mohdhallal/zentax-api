package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrInvalidKey is returned when the encryption key is not 32 bytes (AES-256).
var ErrInvalidKey = errors.New("crypto: encryption key must be 32 bytes")

// Encrypt seals plaintext with AES-256-GCM under key (32 bytes), returning a
// base64 string of nonce||ciphertext. Interim field-level encryption for secrets
// like TOTP seeds, until ADR-0006 per-tenant KMS keys replace the single key.
func Encrypt(key, plaintext []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("crypto: read nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a value produced by Encrypt.
func Decrypt(key []byte, encoded string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	sealed, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode ciphertext: %w", err)
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce, ciphertext := sealed[:ns], sealed[ns:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
