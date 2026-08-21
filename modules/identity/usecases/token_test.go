package usecases

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

func newTokenUC() (*UseCases, *domain.UserRepositoryMock, *domain.TokenRepositoryMock) {
	users := new(domain.UserRepositoryMock)
	tokens := new(domain.TokenRepositoryMock)
	uc := NewUseCases(users, new(domain.SessionRepositoryMock), tokens, new(domain.GrantWriterMock), testSettings())
	return uc, users, tokens
}

func validToken(raw string, now time.Time) *domain.APIToken {
	return &domain.APIToken{
		ID: "tok-1", TokenHash: crypto.HashToken(raw), UserID: "sa-1", TenantID: "ten-1",
		Label: "test", ExpiresAt: now.Add(24 * time.Hour),
	}
}

func serviceUser() *domain.User {
	return &domain.User{ID: "sa-1", TenantID: "ten-1", Name: "agent", Kind: domain.KindService, Status: "active"}
}

func TestAuthenticateToken_Valid(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	uc, users, tokens := newTokenUC()
	raw := domain.TokenPrefix + "secret"

	tokens.On("GetByTokenHash", ctx, crypto.HashToken(raw)).Return(validToken(raw, time.Now()), nil).Once()
	users.On("GetByID", ctx, "sa-1").Return(serviceUser(), nil).Once()
	tokens.On("TouchLastUsed", ctx, "tok-1", mock.AnythingOfType("time.Time")).Return(nil).Once()

	token, user, err := uc.AuthenticateToken(ctx, raw)
	require.NoError(t, err)
	assert.Equal(t, "ten-1", token.TenantID)
	assert.True(t, user.IsService())
}

func TestAuthenticateToken_RejectsWrongPrefixRevokedExpiredAndHumans(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	// wrong prefix: rejected before any lookup.
	uc, _, _ := newTokenUC()
	_, _, err := uc.AuthenticateToken(ctx, "not-a-zentax-token")
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)

	raw := domain.TokenPrefix + "secret"

	// revoked
	uc, _, tokens := newTokenUC()
	revoked := validToken(raw, time.Now())
	at := time.Now().Add(-time.Hour)
	revoked.RevokedAt = &at
	tokens.On("GetByTokenHash", ctx, crypto.HashToken(raw)).Return(revoked, nil).Once()
	_, _, err = uc.AuthenticateToken(ctx, raw)
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)

	// expired
	uc, _, tokens = newTokenUC()
	expired := validToken(raw, time.Now().Add(-48*time.Hour))
	tokens.On("GetByTokenHash", ctx, crypto.HashToken(raw)).Return(expired, nil).Once()
	_, _, err = uc.AuthenticateToken(ctx, raw)
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)

	// a token pointing at a HUMAN user is rejected (tokens are machine-only).
	uc, users, tokens := newTokenUC()
	tokens.On("GetByTokenHash", ctx, crypto.HashToken(raw)).Return(validToken(raw, time.Now()), nil).Once()
	human := serviceUser()
	human.Kind = domain.KindHuman
	users.On("GetByID", ctx, "sa-1").Return(human, nil).Once()
	_, _, err = uc.AuthenticateToken(ctx, raw)
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
}
