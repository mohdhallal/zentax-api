package usecases

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
)

func sampleFields() domain.Fields {
	return domain.Fields{{ID: "f-a", Name: "A", FieldType: domain.FieldTypeNumeric, Mandatory: true}}
}

func sampleTemplate(category string) *domain.DataTemplate {
	return &domain.DataTemplate{
		ID: "11111111-1111-1111-1111-111111111111", Name: "Custom VAT", TemplateType: "VAT",
		Category: category, Fields: sampleFields(), CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
}

func TestDTCreate_ForcesCustomCategoryAndValidates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.DataTemplateRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateDataTemplateInput{Name: "Custom VAT", TemplateType: "VAT", Category: "predefined", Fields: sampleFields()}
	want := in
	want.Category = domain.CategoryCustom // never trusted from the caller
	repo.On("Create", ctx, want).Return(sampleTemplate("custom"), nil).Once()

	_, err := uc.Create(ctx, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestDTCreate_RejectsInvalidFields(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.DataTemplateRepositoryMock)
	uc := NewUseCases(repo)

	cases := map[string]domain.Fields{
		"empty":         {},
		"duplicate id":  {{ID: "x", Name: "X", FieldType: "text"}, {ID: "x", Name: "Y", FieldType: "text"}},
		"nv on text":    {{ID: "x", Name: "X", FieldType: "text", NumericValidation: &domain.NumericValidation{}}},
		"min > max":     {{ID: "x", Name: "X", FieldType: "numeric", NumericValidation: &domain.NumericValidation{Min: f64(2), Max: f64(1)}}},
		"unknown type":  {{ID: "x", Name: "X", FieldType: "money"}},
		"decimals > 10": {{ID: "x", Name: "X", FieldType: "numeric", NumericValidation: &domain.NumericValidation{DecimalPlaces: intp(11)}}},
	}
	for name, fields := range cases {
		_, err := uc.Create(ctx, domain.CreateDataTemplateInput{Name: "T", TemplateType: "VAT", Fields: fields})
		assert.IsType(t, &apperrors.ValidationError{}, err, name)
	}
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestDTUpdate_PredefinedIsImmutable(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.DataTemplateRepositoryMock)
	uc := NewUseCases(repo)

	tpl := sampleTemplate(domain.CategoryPredefined)
	repo.On("GetById", ctx, tpl.ID).Return(tpl, nil).Once()

	_, err := uc.Update(ctx, tpl.ID, domain.UpdateDataTemplateInput{Name: "X", TemplateType: "VAT", Fields: sampleFields()})
	assert.IsType(t, &apperrors.ForbiddenError{}, err)
	assert.EqualError(t, err, domain.MsgPredefinedImmutable)
	repo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
}

func TestDTUpdate_CustomOK(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.DataTemplateRepositoryMock)
	uc := NewUseCases(repo)

	tpl := sampleTemplate(domain.CategoryCustom)
	in := domain.UpdateDataTemplateInput{Name: "Renamed", TemplateType: "CIT", Fields: sampleFields()}
	repo.On("GetById", ctx, tpl.ID).Return(tpl, nil).Once()
	repo.On("Update", ctx, tpl.ID, in).Return(tpl, nil).Once()

	got, err := uc.Update(ctx, tpl.ID, in)
	require.NoError(t, err)
	assert.Equal(t, tpl, got)
	// Same field ids/types → compatible → the reference count is never consulted.
	repo.AssertNotCalled(t, "ReferenceCount", mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
}

// An in-use template keeps its existing field ids and types: removing or
// retyping one → 409; adding a field, renaming, making mandatory → fine.
func TestDTUpdate_InUseFieldsFrozen(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.DataTemplateRepositoryMock)
	uc := NewUseCases(repo)
	tpl := sampleTemplate(domain.CategoryCustom)

	removed := domain.UpdateDataTemplateInput{Name: tpl.Name, TemplateType: "VAT",
		Fields: domain.Fields{{ID: "f-b", Name: "B", FieldType: domain.FieldTypeText}}}
	retyped := domain.UpdateDataTemplateInput{Name: tpl.Name, TemplateType: "VAT",
		Fields: domain.Fields{{ID: "f-a", Name: "A", FieldType: domain.FieldTypeText}}}
	additive := domain.UpdateDataTemplateInput{Name: "Renamed", TemplateType: "VAT",
		Fields: domain.Fields{
			{ID: "f-a", Name: "A (renamed)", FieldType: domain.FieldTypeNumeric, Mandatory: false},
			{ID: "f-b", Name: "B", FieldType: domain.FieldTypeDate, Mandatory: true},
		}}

	repo.On("GetById", ctx, tpl.ID).Return(tpl, nil).Times(3)
	repo.On("ReferenceCount", ctx, tpl.ID).Return(4, nil).Twice()
	repo.On("Update", ctx, tpl.ID, additive).Return(tpl, nil).Once()

	_, err := uc.Update(ctx, tpl.ID, removed)
	assert.IsType(t, &apperrors.ConflictError{}, err)
	_, err = uc.Update(ctx, tpl.ID, retyped)
	assert.IsType(t, &apperrors.ConflictError{}, err)
	_, err = uc.Update(ctx, tpl.ID, additive)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestDTUpdate_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.DataTemplateRepositoryMock)
	uc := NewUseCases(repo)
	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	_, err := uc.Update(ctx, "missing", domain.UpdateDataTemplateInput{Name: "X", TemplateType: "VAT", Fields: sampleFields()})
	assert.IsType(t, &apperrors.NotFoundError{}, err)
}

func TestDTDelete_PredefinedForbidden_InUseConflict_CustomOK(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.DataTemplateRepositoryMock)
	uc := NewUseCases(repo)

	pre := sampleTemplate(domain.CategoryPredefined)
	repo.On("GetById", ctx, pre.ID).Return(pre, nil).Once()
	err := uc.Delete(ctx, pre.ID)
	assert.IsType(t, &apperrors.ForbiddenError{}, err)

	inUse := sampleTemplate(domain.CategoryCustom)
	inUse.ID = "22222222-2222-2222-2222-222222222222"
	repo.On("GetById", ctx, inUse.ID).Return(inUse, nil).Once()
	repo.On("ReferenceCount", ctx, inUse.ID).Return(2, nil).Once()
	err = uc.Delete(ctx, inUse.ID)
	assert.IsType(t, &apperrors.ConflictError{}, err)
	assert.EqualError(t, err, domain.MsgTemplateInUse)

	free := sampleTemplate(domain.CategoryCustom)
	free.ID = "33333333-3333-3333-3333-333333333333"
	repo.On("GetById", ctx, free.ID).Return(free, nil).Once()
	repo.On("ReferenceCount", ctx, free.ID).Return(0, nil).Once()
	repo.On("Delete", ctx, free.ID).Return(true, nil).Once()
	require.NoError(t, uc.Delete(ctx, free.ID))

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()
	assert.IsType(t, &apperrors.NotFoundError{}, uc.Delete(ctx, "missing"))
	repo.AssertExpectations(t)
}

func TestDTSeedPredefined_InsertsMissingOnly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.DataTemplateRepositoryMock)
	uc := NewUseCases(repo)

	predefined := domain.CategoryPredefined
	filter := domain.ListDataTemplatesArgs{Category: &predefined}
	everything := domain.ListDataTemplatesArgs{}

	// First call: the tenant already has "VAT Return" — as a CUSTOM template
	// (names are unique per tenant across categories) → only CIT + WHT are
	// inserted, and the collision does not fail the seed.
	existing := []domain.DataTemplate{{ID: "vat", Name: "VAT Return", Category: domain.CategoryCustom}}
	repo.On("List", ctx, everything).Return(existing, nil).Once()
	var created []string
	repo.On("Create", ctx, mock.MatchedBy(func(in domain.CreateDataTemplateInput) bool {
		return in.Category == predefined && in.Name != "VAT Return"
	})).Run(func(args mock.Arguments) {
		created = append(created, args.Get(1).(domain.CreateDataTemplateInput).Name)
	}).Return(sampleTemplate(predefined), nil).Twice()
	all := []domain.DataTemplate{
		{ID: "cit", Name: "Corporate Income Tax", Category: predefined},
		{ID: "wht", Name: "Withholding Tax", Category: predefined},
	}
	repo.On("List", ctx, filter).Return(all, nil).Once()

	got, err := uc.SeedPredefined(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"Corporate Income Tax", "Withholding Tax"}, created)
	assert.Len(t, got, 2)

	// Second call: every name present → no Create at all, same list back.
	repo.On("List", ctx, everything).Return(append(existing, all...), nil).Once()
	repo.On("List", ctx, filter).Return(all, nil).Once()
	got, err = uc.SeedPredefined(ctx)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	repo.AssertExpectations(t)
	repo.AssertNumberOfCalls(t, "Create", 2)
}

func TestDTGetById_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.DataTemplateRepositoryMock)
	uc := NewUseCases(repo)
	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	got, err := uc.GetById(ctx, "missing")
	assert.Nil(t, got)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
}

func TestDTList_NeverNil(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.DataTemplateRepositoryMock)
	uc := NewUseCases(repo)
	repo.On("List", ctx, domain.ListDataTemplatesArgs{}).Return(nil, nil).Once()

	got, err := uc.List(ctx, domain.ListDataTemplatesArgs{})
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func f64(v float64) *float64 { return &v }
func intp(v int) *int        { return &v }
