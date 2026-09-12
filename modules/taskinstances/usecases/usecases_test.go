package usecases

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

func sampleTaskInstance() *domain.TaskInstance {
	return &domain.TaskInstance{
		ID: "ti-1", WorkflowID: "wf-1", WorkflowTaskID: "wt-1", PeriodCode: "M1",
		Name: "Prepare", TaskType: "preparation", Status: "not_started",
		DueDate:        dateonly.New(2025, 2, 10),
		PeriodEndDate:  dateonly.New(2025, 1, 31),
		FilingDeadline: dateonly.New(2025, 2, 15),
		TaxDataStatus:  "draft",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
}

func TestTIGetById_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	result, err := uc.GetById(ctx, "missing")
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestTIGetById_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	ti := sampleTaskInstance()
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()

	result, err := uc.GetById(ctx, ti.ID)
	require.NoError(t, err)
	assert.Equal(t, ti, result)
	repo.AssertExpectations(t)
}

func TestTIUpdate_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	repo.On("GetById", ctx, "missing").Return(nil, nil).Once()

	result, err := uc.Update(ctx, "missing", domain.UpdateTaskInstanceInput{Status: "in_progress"})
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t) // Update never reached
}

func TestTIUpdate_DefaultsTaxDataStatusAndSucceeds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	ti := sampleTaskInstance()                                  // status not_started → not locked
	in := domain.UpdateTaskInstanceInput{Status: "in_progress"} // no taxDataStatus
	want := in
	want.TaxDataStatus = "draft"

	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()
	repo.On("Update", ctx, ti.ID, want).Return(ti, nil).Once()

	result, err := uc.Update(ctx, ti.ID, in)
	require.NoError(t, err)
	assert.Equal(t, ti, result)
	repo.AssertExpectations(t)
}

func TestTIUpdate_ApprovedIsImmutable(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	ti := sampleTaskInstance()
	approver := "user-9"
	ti.ApprovedBy = &approver // approved → locked (ADR-0018)

	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()

	result, err := uc.Update(ctx, ti.ID, domain.UpdateTaskInstanceInput{Status: "in_progress"})
	assert.Nil(t, result)
	assert.IsType(t, &apperrors.ConflictError{}, err)
	repo.AssertExpectations(t) // Update never reached — the record is frozen
}

// The whitelist records the dates, the assignee and the status, withholds the
// notes, and summarises the tax record as a shape: how many figures it held on
// each side and which keys moved — never an amount (ADR-0008).
func TestTIAuditValues_RecordsTheEditAndShapesTheFigures(t *testing.T) {
	t.Parallel()

	before := sampleTaskInstance()
	before.TaxData = domain.TaxData{"salesTotal": 1000.0, "vatDue": 190.0}

	after := sampleTaskInstance()
	assignee := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	notes := "call Jane Doe on 0170 1234567"
	after.Status = "in_progress"
	after.DueDate = dateonly.New(2025, 11, 30)
	after.AssigneeID = &assignee
	after.Notes = &notes
	after.TaxDataStatus = "final"
	after.TaxData = domain.TaxData{"salesTotal": 1000.0, "vatDue": 250.0, "carryForward": 12.5}

	details := withTaxDataChange(
		audit.Changes(auditValues(before), auditValues(after)),
		before.TaxData, after.TaxData)
	encoded, err := json.Marshal(details)
	require.NoError(t, err)

	assert.Contains(t, string(encoded), `"status":{"from":"not_started","to":"in_progress"}`)
	assert.Contains(t, string(encoded), `"dueDate":{"from":"2025-02-10","to":"2025-11-30"}`)
	assert.Contains(t, string(encoded), `"taxDataStatus":{"from":"draft","to":"final"}`)
	assert.Contains(t, string(encoded), `"assigneeId":{"from":null,"to":"`+assignee+`"}`)
	assert.Contains(t, string(encoded), `"notes":{"from":"empty","to":"set"}`)
	assert.NotContains(t, string(encoded), "Jane Doe")

	// The figures moved: two keys, counted and named, values withheld. The shape
	// sits beside `fields`, which stays a strict field -> {from, to} map.
	assert.Contains(t, string(encoded),
		`"taxData":{"changedKeys":["carryForward","vatDue"],"figuresAfter":3,"figuresBefore":2}`)
	assert.NotContains(t, string(encoded), "190")
	assert.NotContains(t, string(encoded), "250")
	assert.NotContains(t, string(encoded), `"salesTotal"`) // unchanged, so absent
}

// A rewrite that moves nothing records an empty envelope, and a tax-data edit
// alone still records one — the shape is added even when audit.Changes found
// nothing whitelisted.
func TestTIAuditValues_TaxDataAloneStillRecordsAChange(t *testing.T) {
	t.Parallel()

	same := sampleTaskInstance()
	assert.Nil(t, withTaxDataChange(audit.Changes(auditValues(same), auditValues(same)), nil, nil))

	details := withTaxDataChange(
		audit.Changes(auditValues(same), auditValues(same)),
		domain.TaxData{"salesTotal": 1000.0}, domain.TaxData{"salesTotal": 2000.0})
	encoded, err := json.Marshal(details)
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"taxData":{"figuresBefore":1,"figuresAfter":1,"changedKeys":["salesTotal"]}}`,
		string(encoded))
}

// A tax-data key is a template field id the tenant chose, so only an
// identifier-shaped one is quoted; anything else is a string somebody typed.
func TestTIAuditTaxDataKey_QuotesOnlyIdentifierShapes(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"salesTotal", "vat_due", "box-1", "line.3", "Q4"} {
		assert.Equal(t, key, auditTaxDataKey(key))
	}
	for _, key := range []string{
		"", "Jane Doe", "jane.doe@example.com", "12 Hauptstrasse, Berlin",
		"+49 170 1234567", strings.Repeat("x", maxTaxDataKeyLen+1),
	} {
		assert.Equal(t, redactedKey, auditTaxDataKey(key), "key %q must not be quoted", key)
	}
}

func TestTIList_Aggregates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := new(domain.TaskInstanceRepositoryMock)
	uc := NewUseCases(repo)

	listArgs := sharedtypes.ListArgs{Limit: 20, Offset: 0}
	items := []domain.TaskInstance{*sampleTaskInstance()}
	repo.On("List", ctx, listArgs).Return(items, nil).Once()
	repo.On("GetTotal", ctx, listArgs.Filters).Return(1, nil).Once()

	result, err := uc.List(ctx, listArgs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Total)
	assert.Len(t, result.Items, 1)
	repo.AssertExpectations(t)
}
