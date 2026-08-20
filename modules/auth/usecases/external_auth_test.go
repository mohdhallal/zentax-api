package usecases

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/auth/domain"
)

func TestExternalAuth_StoreExternalAPIKey_Success(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := domain.NewNexusAccountAPIKeyRepoMock(t)
	uc := NewExternalAuth(repo)

	input := domain.StoreNexusAPIKeyInput{NexusAccountID: 42, APIKey: "k", APISecret: "s"}
	expected := &domain.NexusAccountAPIKey{ID: 1, NexusAccountID: 42, APIKey: "k", APISecret: "s", CreatedAt: time.Now(), UpdatedAt: time.Now()}

	repo.EXPECT().Store(ctx, input).Return(expected, nil)

	result, err := uc.StoreExternalAPIKey(ctx, input)

	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestExternalAuth_StoreExternalAPIKey_RepoError_Propagates(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := domain.NewNexusAccountAPIKeyRepoMock(t)
	uc := NewExternalAuth(repo)

	input := domain.StoreNexusAPIKeyInput{NexusAccountID: 99}
	repoErr := errors.New("unique violation")

	repo.EXPECT().Store(ctx, input).Return(nil, repoErr)

	result, err := uc.StoreExternalAPIKey(ctx, input)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, repoErr)
}

func TestExternalAuth_GetByNexusAccountID_Success(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := domain.NewNexusAccountAPIKeyRepoMock(t)
	uc := NewExternalAuth(repo)

	expected := &domain.NexusAccountAPIKey{ID: 5, NexusAccountID: 7, APIKey: "key"}

	repo.EXPECT().GetByNexusAccountID(ctx, 7).Return(expected, nil)

	result, err := uc.GetByNexusAccountID(ctx, 7)

	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestExternalAuth_GetByNexusAccountID_NilRecord_ReturnsNotFound(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := domain.NewNexusAccountAPIKeyRepoMock(t)
	uc := NewExternalAuth(repo)

	repo.EXPECT().GetByNexusAccountID(ctx, 404).Return(nil, nil)

	result, err := uc.GetByNexusAccountID(ctx, 404)

	assert.Nil(t, result)
	require.Error(t, err)

	var notFound *apperrors.NotFoundError
	assert.ErrorAs(t, err, &notFound)
	assert.Contains(t, err.Error(), "404")
}

func TestExternalAuth_GetByNexusAccountID_RepoError_Propagates(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := domain.NewNexusAccountAPIKeyRepoMock(t)
	uc := NewExternalAuth(repo)

	repoErr := errors.New("timeout")

	repo.EXPECT().GetByNexusAccountID(ctx, 1).Return(nil, repoErr)

	result, err := uc.GetByNexusAccountID(ctx, 1)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, repoErr)
}
