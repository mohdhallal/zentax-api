package usecases

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

func sampleObligationType() *domain.ObligationType {
	return &domain.ObligationType{
		ID:        "22222222-2222-2222-2222-222222222222",
		Name:      "VAT Return",
		Code:      "VAT-RET",
		Category:  "custom",
		Template:  "VAT",
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestOTCreate_DefaultsCategory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateObligationTypeInput{Name: "VAT Return", Code: "VAT-RET", Template: "VAT"}
	want := in
	want.Category = "custom" // repo must receive the default

	repo.On("Create", ctx, want).Return(sampleObligationType(), nil).Once()

	_, err := uc.Create(ctx, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestOTCreate_PassesCategory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateObligationTypeInput{Name: "CIT", Code: "CIT-A", Template: "CIT", Category: "predefined"}
	repo.On("Create", ctx, in).Return(sampleObligationType(), nil).Once()

	_, err := uc.Create(ctx, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestOTGetById_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	result, err := uc.GetById(ctx, "missing")
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestOTGetById_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	ot := sampleObligationType()
	repo.On("GetById", ctx, ot.ID).Return(ot, nil).Once()

	result, err := uc.GetById(ctx, ot.ID)
	require.NoError(t, err)
	assert.Equal(t, ot, result)
	repo.AssertExpectations(t)
}

func TestOTGetById_RepoError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "x").Return(nil, assert.AnError).Once()

	_, err := uc.GetById(ctx, "x")
	assert.ErrorIs(t, err, assert.AnError)
	repo.AssertExpectations(t)
}

// The audit pre-read also answers the 404: an unknown id never reaches the
// write.
func TestOTUpdate_MissingRowIsNotFoundBeforeTheWrite(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	result, err := uc.Update(ctx, "missing", domain.UpdateObligationTypeInput{Name: "VAT", Code: "VAT-RET", Template: "VAT"})
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
	repo.AssertNotCalled(t, "Update", ctx, "missing", mock.Anything)
}

func TestOTUpdate_DefaultsCategoryAndStatus(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	ot := sampleObligationType()
	in := domain.UpdateObligationTypeInput{Name: "VAT", Code: "VAT-RET", Template: "VAT"} // no category/status
	want := in
	want.Category = "custom"
	want.Status = "active"

	repo.On("GetById", ctx, ot.ID).Return(sampleObligationType(), nil).Once()
	repo.On("Update", ctx, ot.ID, want).Return(ot, nil).Once()

	result, err := uc.Update(ctx, ot.ID, in)
	require.NoError(t, err)
	assert.Equal(t, ot, result)
	repo.AssertExpectations(t)
}

func TestOTUpdate_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	ot := sampleObligationType()
	in := domain.UpdateObligationTypeInput{
		Name: "VAT", Code: "VAT-RET", Template: "VAT", Category: "custom", Status: "inactive",
	}
	repo.On("GetById", ctx, ot.ID).Return(sampleObligationType(), nil).Once()
	repo.On("Update", ctx, ot.ID, in).Return(ot, nil).Once()

	result, err := uc.Update(ctx, ot.ID, in)
	require.NoError(t, err)
	assert.Equal(t, ot, result)
	repo.AssertExpectations(t)
}

func TestOTDelete_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	err := uc.Delete(ctx, "missing")
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
	repo.AssertNotCalled(t, "Delete", ctx, "missing")
}

func TestOTDelete_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "ot1").Return(sampleObligationType(), nil).Once()
	repo.On("Delete", ctx, "ot1").Return(true, nil).Once()

	err := uc.Delete(ctx, "ot1")
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

// The whitelist quotes the machine fields that say what kind of obligation the
// definition IS, and withholds every string the contract lets a caller type —
// including the code, which is `max=50` free text (ADR-0008).
func TestOTAuditValues_QuotesClassificationAndWithholdsTypedText(t *testing.T) {
	t.Parallel()

	before := sampleObligationType()
	description := "Monthly domestic VAT return"
	before.Description = &description

	after := sampleObligationType()
	after.Code = "VAT-RET-2"
	after.Template = "CIT"
	after.Status = "inactive"
	after.Description = nil

	encoded, err := json.Marshal(audit.Changes(auditValues(before), auditValues(after)))
	require.NoError(t, err)

	assert.Contains(t, string(encoded), `"template":{"from":"VAT","to":"CIT"}`)
	assert.Contains(t, string(encoded), `"status":{"from":"active","to":"inactive"}`)
	assert.Contains(t, string(encoded), `"code":{"from":"set","to":"set"}`)
	assert.Contains(t, string(encoded), `"description":{"from":"set","to":"empty"}`)
	assert.NotContains(t, string(encoded), "VAT-RET")    // the code itself, never
	assert.NotContains(t, string(encoded), "VAT Return") // nor the name
	assert.NotContains(t, string(encoded), description)
	assert.NotContains(t, string(encoded), `"category"`) // unchanged, so absent
}

func TestOTList_Aggregates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.ObligationTypeRepositoryMock)
	uc := NewUseCases(repo)

	listArgs := sharedtypes.ListArgs{Limit: 20, Offset: 0}
	items := []domain.ObligationType{*sampleObligationType()}
	repo.On("List", ctx, listArgs).Return(items, nil).Once()
	repo.On("GetTotal", ctx, listArgs.Filters).Return(1, nil).Once()

	result, err := uc.List(ctx, listArgs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Total)
	assert.Len(t, result.Items, 1)
	repo.AssertExpectations(t)
}
