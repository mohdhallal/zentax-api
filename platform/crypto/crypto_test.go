package crypto

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPassword_HashVerify(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct horse battery staple")
	require.NoError(t, err)
	assert.Contains(t, hash, "$argon2id$")

	ok, err := VerifyPassword("correct horse battery staple", hash)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = VerifyPassword("wrong password", hash)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestPassword_UniqueSalt(t *testing.T) {
	t.Parallel()
	h1, err := HashPassword("same")
	require.NoError(t, err)
	h2, err := HashPassword("same")
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2)
}

func TestPassword_InvalidHash(t *testing.T) {
	t.Parallel()
	_, err := VerifyPassword("x", "not-a-hash")
	assert.ErrorIs(t, err, ErrInvalidHash)
}

func TestSecret_EncryptDecrypt(t *testing.T) {
	t.Parallel()
	key := make([]byte, 32)
	_, _ = rand.Read(key)

	ct, err := Encrypt(key, []byte("TOTP-SECRET"))
	require.NoError(t, err)

	pt, err := Decrypt(key, ct)
	require.NoError(t, err)
	assert.Equal(t, "TOTP-SECRET", string(pt))

	badKey := make([]byte, 32)
	_, _ = rand.Read(badKey)
	_, err = Decrypt(badKey, ct)
	assert.Error(t, err)

	_, err = Encrypt([]byte("short"), []byte("x"))
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestToken(t *testing.T) {
	t.Parallel()
	tok, err := NewSessionToken()
	require.NoError(t, err)
	assert.NotEmpty(t, tok)

	tok2, err := NewSessionToken()
	require.NoError(t, err)
	assert.NotEqual(t, tok, tok2)

	assert.Equal(t, HashToken(tok), HashToken(tok))
	assert.NotEqual(t, HashToken(tok), HashToken(tok2))
}
