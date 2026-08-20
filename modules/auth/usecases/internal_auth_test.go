package usecases

import (
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/modules/auth/domain"
)

// makeSecret returns (secret, storedHash) for the given raw bytes.
func makeSecret(raw []byte) (secret, storedHash string) {
	secret = base64.RawURLEncoding.EncodeToString(raw)
	hash := sha512.Sum512(raw)
	storedHash = base64.RawURLEncoding.EncodeToString(hash[:])
	return
}

func TestInternalAuth_Validate_Success(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := domain.NewInternalAPIKeyRepoMock(t)
	uc := NewInternalAuth(repo)

	keyUUID := uuid.New()
	secret, storedHash := makeSecret([]byte("raw-secret-bytes"))

	apiKey := &domain.InternalAPIKey{
		Key:        keyUUID,
		AppName:    "payments-service",
		SecretHash: storedHash,
	}

	repo.EXPECT().GetByKey(ctx, keyUUID).Return(apiKey, nil)

	result, err := uc.Validate(ctx, keyUUID.String(), secret)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "payments-service", result.ServiceName)
}

func TestInternalAuth_Validate_InvalidKeyFormat(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := domain.NewInternalAPIKeyRepoMock(t)
	uc := NewInternalAuth(repo)

	result, err := uc.Validate(ctx, "not-a-uuid", "any-secret")

	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), domain.ErrMsgInvalidKeyFormat)
}

func TestInternalAuth_Validate_RepoError_Propagates(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := domain.NewInternalAPIKeyRepoMock(t)
	uc := NewInternalAuth(repo)

	keyUUID := uuid.New()
	repoErr := errors.New("db connection lost")

	repo.EXPECT().GetByKey(ctx, keyUUID).Return(nil, repoErr)

	result, err := uc.Validate(ctx, keyUUID.String(), "any")

	assert.Nil(t, result)
	assert.ErrorIs(t, err, repoErr)
}

func TestInternalAuth_Validate_KeyNotFound_ReturnsInvalidCredentials(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := domain.NewInternalAPIKeyRepoMock(t)
	uc := NewInternalAuth(repo)

	keyUUID := uuid.New()

	repo.EXPECT().GetByKey(ctx, keyUUID).Return(nil, nil)

	result, err := uc.Validate(ctx, keyUUID.String(), "any")

	assert.Nil(t, result)
	require.Error(t, err)
	assert.Equal(t, domain.ErrMsgInvalidCredentials, err.Error())
}

func TestInternalAuth_Validate_WrongSecret_ReturnsInvalidCredentials(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := domain.NewInternalAPIKeyRepoMock(t)
	uc := NewInternalAuth(repo)

	keyUUID := uuid.New()
	_, storedHash := makeSecret([]byte("correct-raw-secret"))
	wrongRaw := []byte("wrong-raw-secret")
	wrongSecret := base64.RawURLEncoding.EncodeToString(wrongRaw)

	apiKey := &domain.InternalAPIKey{
		Key:        keyUUID,
		AppName:    "svc",
		SecretHash: storedHash,
	}

	repo.EXPECT().GetByKey(ctx, keyUUID).Return(apiKey, nil)

	result, err := uc.Validate(ctx, keyUUID.String(), wrongSecret)

	assert.Nil(t, result)
	require.Error(t, err)
	assert.Equal(t, domain.ErrMsgInvalidCredentials, err.Error())
}

// compareSecretHash is unexported — test directly in same package.

func TestCompareSecretHash_MatchingSecret_ReturnsTrue(t *testing.T) {
	t.Parallel()

	raw := []byte("super-secret-raw")
	secret, storedHash := makeSecret(raw)

	assert.True(t, compareSecretHash(secret, storedHash))
}

func TestCompareSecretHash_WrongSecret_ReturnsFalse(t *testing.T) {
	t.Parallel()

	_, storedHash := makeSecret([]byte("correct"))
	wrongSecret := base64.RawURLEncoding.EncodeToString([]byte("wrong"))

	assert.False(t, compareSecretHash(wrongSecret, storedHash))
}

func TestCompareSecretHash_InvalidBase64_ReturnsFalse(t *testing.T) {
	t.Parallel()

	assert.False(t, compareSecretHash("!!!invalid-base64!!!", "anything"))
}
