package usecases

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

func strp(s string) *string { return &s }

func TestEntityCreate_RejectsInvalidFiscalConfig(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cases := map[string]struct {
		in   domain.CreateEntityInput
		want string
	}{
		"custom without periods": {
			in:   domain.CreateEntityInput{Name: "A", Country: "DE", FiscalCalendarPattern: "custom"},
			want: "at least one custom period",
		},
		"bad financialYearEnd": {
			in:   domain.CreateEntityInput{Name: "A", Country: "DE", FinancialYearEnd: strp("13-01")},
			want: "financialYearEnd",
		},
		"bad custom start date": {
			in: domain.CreateEntityInput{Name: "A", Country: "DE", FiscalCalendarPattern: "custom", CustomPeriods: domain.CustomPeriods{
				{Code: "P1", Name: "One", StartDate: "1-1", EndDate: "04-30"},
			}},
			want: "customPeriods[0].startDate",
		},
		"bad custom end date": {
			in: domain.CreateEntityInput{Name: "A", Country: "DE", FiscalCalendarPattern: "custom", CustomPeriods: domain.CustomPeriods{
				{Code: "P1", Name: "One", StartDate: "01-01", EndDate: "04-31"},
			}},
			want: "customPeriods[0].endDate",
		},
		"duplicate code": {
			in: domain.CreateEntityInput{Name: "A", Country: "DE", FiscalCalendarPattern: "custom", CustomPeriods: domain.CustomPeriods{
				{Code: "P1", Name: "One", StartDate: "01-01", EndDate: "04-30"},
				{Code: "P1", Name: "Two", StartDate: "05-01", EndDate: "12-31"},
			}},
			want: "not unique",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := new(domain.EntityRepositoryMock)
			uc := NewUseCases(repo)
			_, err := uc.Create(ctx, tc.in)
			require.Error(t, err)
			assert.IsType(t, &apperrors.ValidationError{}, err)
			assert.Contains(t, err.Error(), tc.want)
			repo.AssertNotCalled(t, "Create")
		})
	}
}

func TestEntityCreate_NonCustomMayCarryPeriods(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	in := domain.CreateEntityInput{
		Name: "A", Country: "DE", FiscalCalendarPattern: "standard",
		FiscalWeekEndDay: "saturday", FiscalYearEndRule: "nearest",
		CustomPeriods: domain.CustomPeriods{{Code: "X", Name: "Kept", StartDate: "01-01", EndDate: "12-31"}},
	}
	repo.On("Create", ctx, in).Return(sampleEntity(), nil).Once()
	_, err := uc.Create(ctx, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestEntityUpdate_RejectsInvalidFiscalConfig(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	_, err := uc.Update(ctx, "e1", domain.UpdateEntityInput{Name: "A", Country: "DE", FiscalCalendarPattern: "custom"})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	repo.AssertNotCalled(t, "Update")
}

func TestEntityPeriods_UsesTheEntityCalendar(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)

	// NRF 2023: Saturday nearest 31 Jan → 53 weeks, M12 ends 3 Feb 2024.
	entity := sampleEntity()
	entity.FiscalCalendarPattern = "445"
	entity.FinancialYearEnd = strp("01-31")
	entity.FiscalWeekEndDay = "saturday"
	entity.FiscalYearEndRule = "nearest"
	repo.On("GetById", ctx, entity.ID).Return(entity, nil)

	periods, err := uc.Periods(ctx, entity.ID, "monthly", 2024)
	require.NoError(t, err)
	require.Len(t, periods, 12)
	assert.Equal(t, "M1", periods[0].Code)
	assert.Equal(t, dateonly.New(2023, 1, 29), periods[0].Start)
	assert.Equal(t, dateonly.New(2024, 2, 3), periods[11].End)

	weeks, err := uc.Periods(ctx, entity.ID, "weekly", 2024)
	require.NoError(t, err)
	assert.Len(t, weeks, 53)

	// The engine's message for an unsupported combination is a 400.
	entity.FiscalCalendarPattern = "custom"
	entity.CustomPeriods = domain.CustomPeriods{{Code: "T1", Name: "T1", StartDate: "01-01", EndDate: "12-31"}}
	_, err = uc.Periods(ctx, entity.ID, "weekly", 2024)
	assert.IsType(t, &apperrors.ValidationError{}, err)
	assert.Contains(t, err.Error(), "weekly")

	_, err = uc.Periods(ctx, entity.ID, "fortnightly", 2024)
	assert.IsType(t, &apperrors.ValidationError{}, err)
}

func TestEntityPeriods_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.EntityRepositoryMock)
	uc := NewUseCases(repo)
	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	_, err := uc.Periods(ctx, "missing", "monthly", 2025)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
}

func TestCustomPeriods_ValueScanRoundTrip(t *testing.T) {
	t.Parallel()
	var empty domain.CustomPeriods
	v, err := empty.Value()
	require.NoError(t, err)
	assert.Nil(t, v, "no periods store as SQL NULL")

	original := domain.CustomPeriods{{Code: "T1", Name: "Trimester 1", StartDate: "01-01", EndDate: "04-30"}}
	v, err = original.Value()
	require.NoError(t, err)
	var scanned domain.CustomPeriods
	require.NoError(t, scanned.Scan(v))
	assert.Equal(t, original, scanned)

	require.NoError(t, scanned.Scan(nil))
	assert.Nil(t, scanned)
	assert.Error(t, scanned.Scan(42))
}
