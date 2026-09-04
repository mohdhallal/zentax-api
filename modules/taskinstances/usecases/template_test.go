package usecases

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	dt "github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	workflowsdomain "github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	workflowtasksdomain "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
)

// stubTemplateResolver is the TemplateResolver port over a fixed map.
type stubTemplateResolver struct {
	templates map[string]*dt.DataTemplate
	calls     []string
}

func (s *stubTemplateResolver) ResolveTemplate(_ context.Context, id string) (*dt.DataTemplate, error) {
	s.calls = append(s.calls, id)
	return s.templates[id], nil
}

func f64(v float64) *float64 { return &v }

func testTemplate() *dt.DataTemplate {
	two := 2
	return &dt.DataTemplate{
		ID: "tpl-vat", Name: "VAT Return", TemplateType: "VAT", Category: dt.CategoryPredefined,
		Fields: dt.Fields{
			{ID: "sales", Name: "Total Sales (net)", FieldType: dt.FieldTypeNumeric, Mandatory: true,
				NumericValidation: &dt.NumericValidation{Min: f64(0), AllowDecimals: true, DecimalPlaces: &two}},
			{ID: "net", Name: "Net VAT Payable", FieldType: dt.FieldTypeNumeric},
		},
	}
}

func newTemplatedUC() (*UseCases, *domain.TaskInstanceRepositoryMock, *stubTemplateResolver) {
	repo := new(domain.TaskInstanceRepositoryMock)
	resolver := &stubTemplateResolver{templates: map[string]*dt.DataTemplate{"tpl-vat": testTemplate()}}
	return NewUseCases(repo).WithTemplateResolver(resolver), repo, resolver
}

func templated() *domain.TaskInstance {
	ti := sampleTaskInstance()
	tpl := "tpl-vat"
	ti.DataTemplateID = &tpl
	return ti
}

func TestTIUpdate_TaxDataValidatedAgainstInheritedTemplate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	uc, repo, resolver := newTemplatedUC()
	ti := templated()
	repo.On("GetById", ctx, ti.ID).Return(ti, nil)

	cases := []struct {
		name string
		in   domain.UpdateTaskInstanceInput
		want string
	}{
		{"unknown key", domain.UpdateTaskInstanceInput{Status: "in_progress", TaxData: domain.TaxData{"bogus": 1.0}},
			`tax data field "bogus" is not in the template`},
		{"string for numeric", domain.UpdateTaskInstanceInput{Status: "in_progress", TaxData: domain.TaxData{"sales": "100"}},
			`tax data field "sales" must be a number`},
		{"out of range", domain.UpdateTaskInstanceInput{Status: "in_progress", TaxData: domain.TaxData{"sales": -5.0}},
			`tax data field "sales" must be >= 0`},
		{"too many decimals", domain.UpdateTaskInstanceInput{Status: "in_progress", TaxData: domain.TaxData{"sales": 1.005}},
			`tax data field "sales" must have at most 2 decimal places`},
		{"final without mandatory", domain.UpdateTaskInstanceInput{Status: "in_progress", TaxDataStatus: "final", TaxData: domain.TaxData{"net": 1.0}},
			`tax data is missing mandatory fields: Total Sales (net)`},
	}
	for _, tc := range cases {
		_, err := uc.Update(ctx, ti.ID, tc.in)
		assert.IsType(t, &apperrors.ValidationError{}, err, tc.name)
		assert.EqualError(t, err, tc.want, tc.name)
	}
	repo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
	assert.Equal(t, []string{"tpl-vat", "tpl-vat", "tpl-vat", "tpl-vat", "tpl-vat"}, resolver.calls)
}

func TestTIUpdate_ValidDraftStoresCleanedData(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	uc, repo, _ := newTemplatedUC()
	ti := templated()

	in := domain.UpdateTaskInstanceInput{Status: "in_progress", TaxData: domain.TaxData{"sales": 100.5, "net": nil}}
	want := in
	want.TaxDataStatus = "draft"
	want.TaxData = domain.TaxData{"sales": 100.5} // null clears
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()
	repo.On("Update", ctx, ti.ID, want).Return(ti, nil).Once()

	_, err := uc.Update(ctx, ti.ID, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestTIUpdate_FinalWithMandatoryOK(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	uc, repo, _ := newTemplatedUC()
	ti := templated()

	in := domain.UpdateTaskInstanceInput{Status: "in_progress", TaxDataStatus: "final", TaxData: domain.TaxData{"sales": 100.0}}
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()
	repo.On("Update", ctx, ti.ID, in).Return(ti, nil).Once()

	_, err := uc.Update(ctx, ti.ID, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

// An incoming dataTemplateId wins over the inherited one and is resolved in
// the caller's tenant; an unresolvable one (another tenant's, or gone) → 400.
func TestTIUpdate_IncomingTemplateResolvedOrRejected(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	uc, repo, resolver := newTemplatedUC()
	ti := sampleTaskInstance() // no template inherited
	repo.On("GetById", ctx, ti.ID).Return(ti, nil)

	foreign := "tpl-of-other-tenant"
	_, err := uc.Update(ctx, ti.ID, domain.UpdateTaskInstanceInput{Status: "in_progress", DataTemplateID: &foreign})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	assert.EqualError(t, err, domain.MsgDataTemplateNotFound)

	vat := "tpl-vat"
	_, err = uc.Update(ctx, ti.ID, domain.UpdateTaskInstanceInput{Status: "in_progress", DataTemplateID: &vat, TaxData: domain.TaxData{"bogus": 1.0}})
	assert.EqualError(t, err, `tax data field "bogus" is not in the template`)

	in := domain.UpdateTaskInstanceInput{Status: "in_progress", DataTemplateID: &vat, TaxData: domain.TaxData{"sales": 1.0}}
	want := in
	want.TaxDataStatus = "draft"
	repo.On("Update", ctx, ti.ID, want).Return(ti, nil).Once()
	_, err = uc.Update(ctx, ti.ID, in)
	require.NoError(t, err)
	assert.Equal(t, []string{"tpl-of-other-tenant", "tpl-vat", "tpl-vat"}, resolver.calls)
	repo.AssertExpectations(t)
}

// No template (none inherited, none incoming) or no resolver wired: tax data
// is stored as sent — the port is optional and nil-safe.
func TestTIUpdate_NoTemplateNoValidation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	uc, repo, resolver := newTemplatedUC()
	ti := sampleTaskInstance()
	in := domain.UpdateTaskInstanceInput{Status: "in_progress", TaxData: domain.TaxData{"free": "form"}}
	want := in
	want.TaxDataStatus = "draft"
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()
	repo.On("Update", ctx, ti.ID, want).Return(ti, nil).Once()
	_, err := uc.Update(ctx, ti.ID, in)
	require.NoError(t, err)
	assert.Empty(t, resolver.calls)

	// Inherited template that no longer resolves (deleted → SET NULL race): stored as sent.
	gone := "tpl-gone"
	ti2 := sampleTaskInstance()
	ti2.ID = "ti-2"
	ti2.DataTemplateID = &gone
	repo.On("GetById", ctx, ti2.ID).Return(ti2, nil).Once()
	repo.On("Update", ctx, ti2.ID, want).Return(ti2, nil).Once()
	_, err = uc.Update(ctx, ti2.ID, in)
	require.NoError(t, err)

	// No resolver at all.
	repo2 := new(domain.TaskInstanceRepositoryMock)
	plain := NewUseCases(repo2)
	ti3 := templated()
	repo2.On("GetById", ctx, ti3.ID).Return(ti3, nil).Once()
	repo2.On("Update", ctx, ti3.ID, want).Return(ti3, nil).Once()
	_, err = plain.Update(ctx, ti3.ID, in)
	require.NoError(t, err)
	repo.AssertExpectations(t)
	repo2.AssertExpectations(t)
}

// A save that merely replays the stored record (status / assignee change from
// the UI) is not re-validated — even when the record no longer matches an
// edited template — while a real change to the data still is.
func TestTIUpdate_UnchangedTaxDataNotRevalidated(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	uc, repo, resolver := newTemplatedUC()
	ti := templated()
	ti.TaxData = domain.TaxData{"sales": 10.0, "orphan": "kept"} // "orphan" left by a template edit
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Times(3)

	// Replay → stored untouched, validator not consulted.
	replay := domain.UpdateTaskInstanceInput{Status: "in_progress", TaxData: domain.TaxData{"sales": 10.0, "orphan": "kept"}}
	want := replay
	want.TaxDataStatus = "draft"
	repo.On("Update", ctx, ti.ID, want).Return(ti, nil).Once()
	_, err := uc.Update(ctx, ti.ID, replay)
	require.NoError(t, err)

	// A changed value alongside the orphan → validated, orphan carried through.
	changed := domain.UpdateTaskInstanceInput{Status: "in_progress", TaxData: domain.TaxData{"sales": 12.5, "orphan": "kept"}}
	wantChanged := changed
	wantChanged.TaxDataStatus = "draft"
	repo.On("Update", ctx, ti.ID, wantChanged).Return(ti, nil).Once()
	_, err = uc.Update(ctx, ti.ID, changed)
	require.NoError(t, err)

	// A NEW unknown key is still refused.
	_, err = uc.Update(ctx, ti.ID, domain.UpdateTaskInstanceInput{Status: "in_progress", TaxData: domain.TaxData{"sales": 10.0, "orphan": "kept", "bogus": 1.0}})
	assert.EqualError(t, err, `tax data field "bogus" is not in the template`)
	assert.Equal(t, []string{"tpl-vat", "tpl-vat", "tpl-vat"}, resolver.calls)
	repo.AssertExpectations(t)
}

func TestTISubmit_RequiresMandatoryFieldsNotFinal(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	uc, repo, _ := newTemplatedUC()

	ti := templated()
	ti.ApprovalRequired = true
	ti.Status = "in_progress"
	ti.TaxData = domain.TaxData{"net": 1.0}
	repo.On("GetById", ctx, ti.ID).Return(ti, nil).Once()
	_, err := uc.SubmitForApproval(ctx, ti.ID, "prep-1")
	assert.IsType(t, &apperrors.ValidationError{}, err)
	assert.EqualError(t, err, "tax data is missing mandatory fields: Total Sales (net)")
	repo.AssertNotCalled(t, "SubmitForApproval", mock.Anything, mock.Anything, mock.Anything)

	// Mandatory present, still a draft → submits.
	ok := templated()
	ok.ID = "ti-ok"
	ok.ApprovalRequired = true
	ok.Status = "in_progress"
	ok.TaxDataStatus = "draft"
	ok.TaxData = domain.TaxData{"sales": 10.0}
	repo.On("GetById", ctx, ok.ID).Return(ok, nil).Once()
	repo.On("SubmitForApproval", ctx, ok.ID, "prep-1").Return(ok, nil).Once()
	_, err = uc.SubmitForApproval(ctx, ok.ID, "prep-1")
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

// Generated instances inherit data_template_id from their workflow task.
func TestGenerator_InstancesInheritDataTemplate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	gen, instances, workflows, workflowTasks, entities := newGenerator()

	wf := recurringWorkflow()
	entity := &entitiesdomain.Entity{ID: "ent-1", FiscalCalendarPattern: "standard", FinancialYearEnd: strp("12-31")}
	tplID := "tpl-vat"
	task := workflowtasksdomain.WorkflowTask{
		ID: "wt-1", WorkflowID: "wf-1", Name: "Prepare", TaskType: "preparation",
		DueDateReference: "filing_deadline", DueDateOffsetValue: 5, DueDateOffsetUnit: "days", DueDateOffsetDirection: "before",
		DataTemplateID: &tplID,
	}
	workflows.On("GetById", ctx, "wf-1").Return(wf, nil).Once()
	instances.On("CountByWorkflow", ctx, "wf-1").Return(0, nil).Once()
	entities.On("GetById", ctx, "ent-1").Return(entity, nil).Once()
	workflowTasks.On("ListByWorkflow", ctx, "wf-1").Return([]workflowtasksdomain.WorkflowTask{task}, nil).Once()
	workflows.On("Update", ctx, "wf-1", mock.Anything).Return(wf, nil).Once()
	instances.On("Create", ctx, mock.MatchedBy(func(in domain.CreateTaskInstanceInput) bool {
		return in.DataTemplateID != nil && *in.DataTemplateID == "tpl-vat"
	})).Return(&domain.TaskInstance{}, nil).Times(2)

	count, err := gen.StartWorkflow(ctx, "wf-1", workflowsdomain.TaskOverrides(nil))
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	instances.AssertExpectations(t)
}
