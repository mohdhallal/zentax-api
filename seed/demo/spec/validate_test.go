package spec

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

func ptr[T any](v T) *T { return &v }

// fixture is a minimal but complete dataset: one tenant, a parent/child entity
// pair, one obligation, a recurring workflow with two periods and a project
// workflow. The validation tests mutate a copy of it, so each case states
// exactly one broken invariant.
func fixture() *Spec {
	return &Spec{
		AsOf: dateonly.New(2026, 9, 6),
		ValidityWindow: ValidityWindow{
			From: dateonly.New(2026, 9, 3),
			To:   dateonly.New(2026, 9, 10),
		},
		Tenants: []Tenant{{
			Key:      "acme",
			Slug:     "acme",
			Name:     "Acme Group",
			Timezone: "Europe/Berlin",
			Admin: User{
				Key: "acme.admin", Email: "admin@acme.test", Name: "Admin",
				Role: "tenant_admin", Password: "demo-password-1", Status: StatusActive,
			},
			Users: []User{
				{
					Key: "acme.manager", Email: "manager@acme.test", Name: "Manager",
					Role: "manager", Password: "demo-password-1", Status: StatusActive,
				},
				{
					Key: "acme.preparer", Email: "preparer@acme.test", Name: "Preparer",
					Role: "preparer", ScopeEntity: ptr("acme.de"),
					Password: "demo-password-1", Status: StatusActive,
				},
				{
					Key: "acme.retired", Email: "retired@acme.test", Name: "Retired",
					Role: "viewer", Password: "demo-password-1", Status: StatusDisabled,
				},
			},
			Entities: []Entity{
				{
					Key: "acme.hold", Name: "Acme Holding", LegalName: "Acme Holding NV",
					Country: "Netherlands", TaxResidency: "NL",
					FiscalCalendarPattern: "standard", FinancialYearEnd: "12-31",
				},
				{
					Key: "acme.de", Name: "Acme DE", LegalName: "Acme GmbH",
					Country: "Germany", TaxResidency: "DE", Parent: ptr("acme.hold"),
					FiscalCalendarPattern: "standard", FinancialYearEnd: "12-31",
				},
			},
			ObligationTypes: []ObligationType{{
				Key: "acme.vat", Name: "German VAT", Code: "DE-VAT",
				Template: "VAT", Category: "predefined", Description: "monthly VAT",
			}},
			EntityObligations: []EntityObligation{{
				Key: "acme.de-vat", Entity: "acme.de", ObligationType: "acme.vat",
				Periodicity: "monthly", Currency: "EUR", Jurisdiction: "Germany",
				DeadlineRule: DeadlineRule{
					Type: "period_offset", Reference: "period_end",
					WeekendAdjustment: "next-business-day",
					FilingOffset:      &Offset{Months: 1, Days: 10},
				},
			}},
			Workflows: []Workflow{
				{
					Key: "acme.w1", Name: "VAT DE 2026", Category: CategoryRecurring,
					Entity: ptr("acme.de"), ObligationType: ptr("acme.vat"),
					EntityObligation: ptr("acme.de-vat"), FinancialYear: "2026",
					Periodicity: ptr("monthly"), SelectedPeriods: []string{"M1", "M2"},
					DueDateRule: DueDateRule{
						Reference: "period_end", OffsetUnit: "days", OffsetValue: 10,
						OffsetDirection: "after", WeekendAdjustment: "next-business-day",
					},
					StartDate: dateonly.New(2026, 1, 1),
					EndDate:   dateonly.New(2026, 12, 31),
					Writer:    "acme.preparer", Approver: "acme.manager",
					Lifecycle: Lifecycle{Start: true, FinalStatus: "active"},
					Templates: []TaskTemplate{
						{
							Key: "prepare", Name: "Prepare", TaskType: "preparation", RoleLabel: "Preparer",
							DueDateReference: "period_end", DueDateOffsetValue: 5,
							DueDateOffsetUnit: "days", DueDateOffsetDirection: "after",
							OrderIndex: 1, DataTemplate: ptr("VAT"),
						},
						{
							Key: "review", Name: "Review", TaskType: "review", RoleLabel: "Reviewer",
							ApprovalRequired: true, DueDateReference: "filing_deadline",
							DueDateOffsetValue: 2, DueDateOffsetUnit: "days",
							DueDateOffsetDirection: "before", OrderIndex: 2,
						},
					},
					Instances: []Instance{
						{
							Period: "M1", Task: "prepare", Status: InstanceCompleted, Via: ViaPut,
							Assignee: "acme.preparer", CompletedOn: dateonly.New(2026, 2, 3),
							TaxData: map[string]any{"outputVat": 1000}, TaxDataStatus: "final",
						},
						{
							Period: "M1", Task: "review", Status: InstanceCompleted, Via: ViaApprove,
							Assignee: "acme.manager", CompletedOn: dateonly.New(2026, 2, 6),
							SubmittedOn: dateonly.New(2026, 2, 5),
						},
					},
					Documents: []Document{{
						Period: "M1", Task: "prepare", DocumentType: "final_return",
						Label: "VAT return M1", Kind: "pdf", FileName: "vat-m1.pdf",
						Category: "compliance",
					}},
				},
				{
					Key: "acme.w2", Name: "Transfer pricing dispute", Category: CategoryProject,
					ProjectType: ptr("dispute"), Entity: ptr("acme.de"), FinancialYear: "2026",
					DueDateRule: DueDateRule{
						Reference: "period_end", OffsetUnit: "days",
						OffsetDirection: "after", WeekendAdjustment: "none",
					},
					StartDate: dateonly.New(2026, 3, 1),
					EndDate:   dateonly.New(2026, 6, 30),
					Writer:    "acme.manager", Approver: "acme.admin",
					Lifecycle: Lifecycle{Start: true, FinalStatus: "completed"},
					Templates: []TaskTemplate{{
						Key: "submit", Name: "Submit response", TaskType: "submission",
						RoleLabel: "Preparer", DueDateReference: "period_end",
						DueDateOffsetUnit: "days", DueDateOffsetDirection: "before", OrderIndex: 1,
					}},
					Instances: []Instance{{
						Period: ProjectPeriodCode, Task: "submit", Status: InstanceCompleted,
						Via: ViaPut, Assignee: "acme.manager", CompletedOn: dateonly.New(2026, 6, 20),
					}},
				},
			},
		}},
		ExpectedAsOf: map[string]map[string]Expectations{
			"2026-09-06": {"acme": {
				WorkflowStats: map[string]WorkflowStatsExpectation{
					"acme.w1": {TotalTasks: 4, CompletedTasks: 2, CompletionPercent: 50},
				},
			}},
		},
	}
}

func TestValidate_FixtureIsValid(t *testing.T) {
	t.Parallel()
	require.NoError(t, fixture().Validate())
}

// TestValidate_Invariants breaks one invariant per case and expects it named.
func TestValidate_Invariants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		break_  func(*Spec)
		wantErr string
	}{
		{
			name:    "duplicate tenant key",
			break_:  func(s *Spec) { s.Tenants = append(s.Tenants, s.Tenants[0]) },
			wantErr: "duplicate tenant key",
		},
		{
			name: "duplicate user key",
			break_: func(s *Spec) {
				s.Tenants[0].Users[1].Key = s.Tenants[0].Users[0].Key
			},
			wantErr: "duplicate user key",
		},
		{
			name: "e-mail reused across tenants",
			break_: func(s *Spec) {
				other := s.Tenants[0]
				other.Key, other.Slug = "globex", "globex"
				s.Tenants = append(s.Tenants, other)
			},
			wantErr: "is already used by",
		},
		{
			name:    "password below the accept-invite minimum",
			break_:  func(s *Spec) { s.Tenants[0].Users[0].Password = "short" },
			wantErr: "password is 5 characters, the minimum is 12",
		},
		{
			name:    "admin is not a tenant admin",
			break_:  func(s *Spec) { s.Tenants[0].Admin.Role = "manager" },
			wantErr: "admin role is",
		},
		{
			name:    "scope entity does not resolve",
			break_:  func(s *Spec) { s.Tenants[0].Users[1].ScopeEntity = ptr("acme.nowhere") },
			wantErr: "scopeEntity \"acme.nowhere\" does not resolve",
		},
		{
			name:    "unknown role",
			break_:  func(s *Spec) { s.Tenants[0].Users[0].Role = "wizard" },
			wantErr: "role: \"wizard\" is not one of",
		},
		{
			name:    "entity parent does not resolve",
			break_:  func(s *Spec) { s.Tenants[0].Entities[1].Parent = ptr("acme.ghost") },
			wantErr: "parent \"acme.ghost\" does not resolve",
		},
		{
			name: "entity hierarchy cycle",
			break_: func(s *Spec) {
				s.Tenants[0].Entities[0].Parent = ptr("acme.de")
			},
			wantErr: "cycle",
		},
		{
			name:    "financialYearEnd is not MM-DD",
			break_:  func(s *Spec) { s.Tenants[0].Entities[0].FinancialYearEnd = "31/12" },
			wantErr: "is not MM-DD",
		},
		{
			name: "duplicate obligation code",
			break_: func(s *Spec) {
				second := s.Tenants[0].ObligationTypes[0]
				second.Key = "acme.vat2"
				s.Tenants[0].ObligationTypes = append(s.Tenants[0].ObligationTypes, second)
			},
			wantErr: "is used twice",
		},
		{
			name:    "unknown obligation template",
			break_:  func(s *Spec) { s.Tenants[0].ObligationTypes[0].Template = "GST" },
			wantErr: "template: \"GST\" is not one of",
		},
		{
			name: "two obligations for the same (entity, obligation type)",
			break_: func(s *Spec) {
				second := s.Tenants[0].EntityObligations[0]
				second.Key = "acme.de-vat-2"
				s.Tenants[0].EntityObligations = append(s.Tenants[0].EntityObligations, second)
			},
			wantErr: "exactly one is allowed",
		},
		{
			name:    "entity obligation points at an unknown entity",
			break_:  func(s *Spec) { s.Tenants[0].EntityObligations[0].Entity = "acme.ghost" },
			wantErr: "entity \"acme.ghost\" does not resolve",
		},
		{
			name: "recurring workflow without an entity obligation",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].EntityObligation = nil
			},
			wantErr: "needs an entityObligation",
		},
		{
			name: "entity obligation belongs to another entity",
			break_: func(s *Spec) {
				s.Tenants[0].EntityObligations[0].Entity = "acme.hold"
			},
			wantErr: "belongs to entity acme.hold",
		},
		{
			name: "project workflow without an end date",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[1].EndDate = dateonly.Date{}
			},
			wantErr: "a project workflow needs an endDate",
		},
		{
			name: "project workflow with selected periods",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[1].SelectedPeriods = []string{"M1"}
			},
			wantErr: "declares no selectedPeriods",
		},
		{
			name: "period code longer than the column",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].SelectedPeriods[0] = "PERIOD-2026-M01"
			},
			wantErr: "the column holds 10",
		},
		{
			name:    "unknown workflow category",
			break_:  func(s *Spec) { s.Tenants[0].Workflows[0].Category = "adhoc" },
			wantErr: "is neither recurring nor project",
		},
		{
			name:    "writer and approver are the same person",
			break_:  func(s *Spec) { s.Tenants[0].Workflows[0].Approver = "acme.preparer" },
			wantErr: "an approval requires two people",
		},
		{
			name:    "writer does not resolve",
			break_:  func(s *Spec) { s.Tenants[0].Workflows[0].Writer = "acme.nobody" },
			wantErr: "writer \"acme.nobody\" does not resolve",
		},
		{
			name:    "approver is disabled",
			break_:  func(s *Spec) { s.Tenants[0].Workflows[0].Approver = "acme.retired" },
			wantErr: "cannot approve",
		},
		{
			name: "unstarted workflow still has instances",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Lifecycle = Lifecycle{Start: false, FinalStatus: "draft"}
			},
			wantErr: "never started",
		},
		{
			name: "started workflow ends as a draft",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Lifecycle.FinalStatus = "draft"
			},
			wantErr: "cannot end as a draft",
		},
		{
			name: "duplicate template key inside a workflow",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Templates[1].Key = "prepare"
			},
			wantErr: "duplicate template key",
		},
		{
			name: "duplicate orderIndex",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Templates[1].OrderIndex = 1
			},
			wantErr: "orderIndex 1 is used twice",
		},
		{
			name: "unknown data template type",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Templates[0].DataTemplate = ptr("TP")
			},
			wantErr: "dataTemplate: \"TP\" is not one of",
		},
		{
			name: "instance references an undeclared template",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].Task = "file"
			},
			wantErr: "task \"file\" is not a template of this workflow",
		},
		{
			name: "instance references an unselected period",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].Period = "M7"
			},
			wantErr: "period \"M7\" is not one of the workflow's periods",
		},
		{
			name: "project instance outside the PROJECT period",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[1].Instances[0].Period = "M1"
			},
			wantErr: "period \"M1\" is not one of the workflow's periods (PROJECT)",
		},
		{
			name: "the same instance twice",
			break_: func(s *Spec) {
				workflow := &s.Tenants[0].Workflows[0]
				workflow.Instances = append(workflow.Instances, workflow.Instances[0])
			},
			wantErr: "declared twice",
		},
		{
			name: "assignee does not exist",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].Assignee = "acme.ghost"
			},
			wantErr: "assignee \"acme.ghost\" does not resolve",
		},
		{
			name: "assignee is disabled",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].Assignee = "acme.retired"
			},
			wantErr: "only an active member can hold a task",
		},
		{
			name: "completed without completedOn",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].CompletedOn = dateonly.Date{}
			},
			wantErr: "no completedOn",
		},
		{
			name: "completedOn on an unfinished instance",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].Status = "in_progress"
			},
			wantErr: "has completedOn but status",
		},
		{
			name: "via=approve without submittedOn",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[1].SubmittedOn = dateonly.Date{}
			},
			wantErr: "via=approve needs submittedOn",
		},
		{
			name: "via=submit must leave the instance pending",
			break_: func(s *Spec) {
				instance := &s.Tenants[0].Workflows[0].Instances[0]
				instance.Via, instance.Status, instance.CompletedOn = ViaSubmit, "in_progress", dateonly.Date{}
			},
			wantErr: "via=submit leaves the instance pending_approval",
		},
		{
			name: "via=put with a submission date",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].SubmittedOn = dateonly.New(2026, 2, 1)
			},
			wantErr: "via=put never submits",
		},
		{
			name: "unknown instance status",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].Status = "done"
			},
			wantErr: "status: \"done\" is not one of",
		},
		{
			name: "taxData without a status",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].TaxDataStatus = ""
			},
			wantErr: "taxData without a taxDataStatus",
		},
		{
			name: "taxDataStatus without data",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[1].TaxDataStatus = "final"
			},
			wantErr: "taxDataStatus without taxData",
		},
		{
			name: "completedAtLocalTime is not a clock time",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].CompletedAtLocalTime = "7am"
			},
			wantErr: "is not HH:MM",
		},
		{
			name: "completedAtUtc contradicts completedOn in the tenant zone",
			break_: func(s *Spec) {
				instance := &s.Tenants[0].Workflows[0].Instances[0]
				instance.CompletedAtLocalTime = "23:30"
				instance.CompletedAtUtc = time.Date(2026, 2, 3, 23, 30, 0, 0, time.UTC)
			},
			wantErr: "completedAtUtc is 2026-02-03T23:30:00Z",
		},
		{
			name: "completedAtUtc without a completion date",
			break_: func(s *Spec) {
				instance := &s.Tenants[0].Workflows[0].Instances[0]
				instance.Status, instance.CompletedOn = "in_progress", dateonly.Date{}
				instance.CompletedAtUtc = time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC)
			},
			wantErr: "completedAtUtc without completedOn",
		},
		{
			name: "completedAtUtc carries an offset",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Instances[0].CompletedAtUtc =
					time.Date(2026, 2, 3, 11, 0, 0, 0, time.FixedZone("CET", 3600))
			},
			wantErr: "is not expressed in UTC",
		},
		{
			name: "document references an undeclared template",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Documents[0].Task = "file"
			},
			wantErr: "task \"file\" is not a template of this workflow",
		},
		{
			name: "unknown document kind",
			break_: func(s *Spec) {
				s.Tenants[0].Workflows[0].Documents[0].Kind = "docx"
			},
			wantErr: "kind: \"docx\" is not one of",
		},
		{
			name: "expectations for an unknown tenant",
			break_: func(s *Spec) {
				s.ExpectedAsOf["2026-09-06"]["globex"] = Expectations{}
			},
			wantErr: "no tenant with that key",
		},
		{
			name: "workflow stats for an unknown workflow",
			break_: func(s *Spec) {
				s.ExpectedAsOf["2026-09-06"]["acme"].WorkflowStats["acme.w9"] = WorkflowStatsExpectation{}
			},
			wantErr: "unknown workflow \"acme.w9\"",
		},
		{
			name: "expectation date is not a date",
			break_: func(s *Spec) {
				s.ExpectedAsOf["soon"] = map[string]Expectations{}
			},
			wantErr: "is not a YYYY-MM-DD date",
		},
		{
			name:    "asOf outside the validity window",
			break_:  func(s *Spec) { s.AsOf = dateonly.New(2026, 10, 1) },
			wantErr: "outside the validity window",
		},
		{
			name: "validity window runs backwards",
			break_: func(s *Spec) {
				s.ValidityWindow.From, s.ValidityWindow.To = s.ValidityWindow.To, s.ValidityWindow.From
			},
			wantErr: "is before from",
		},
		{
			name:    "no tenants at all",
			break_:  func(s *Spec) { s.Tenants = nil },
			wantErr: "no tenants declared",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec := fixture()
			tc.break_(spec)

			err := spec.Validate()

			require.Error(t, err, "the broken invariant went unnoticed")
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestValidationError_ReportsEveryProblem(t *testing.T) {
	t.Parallel()
	spec := fixture()
	spec.Tenants[0].Users[0].Password = "short"
	spec.Tenants[0].Workflows[0].Approver = spec.Tenants[0].Workflows[0].Writer

	err := spec.Validate()
	require.Error(t, err)

	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Len(t, validationErr.Problems, 2, "one run reports every problem, not the first")
	assert.Contains(t, err.Error(), "2 problems")
	assert.Contains(t, err.Error(), "password is 5 characters")
	assert.Contains(t, err.Error(), "requires two people")
}

func TestValidationError_SingleProblemReadsAsOneLine(t *testing.T) {
	t.Parallel()
	err := (&ValidationError{Problems: []string{"tenant acme: slug is required"}}).Error()
	assert.Equal(t, "invalid dataset: tenant acme: slug is required", err)
	assert.False(t, strings.Contains(err, "\n"))
}

func TestValidate_AcceptsAConsistentCompletedAtUtc(t *testing.T) {
	t.Parallel()
	spec := fixture()
	instance := &spec.Tenants[0].Workflows[0].Instances[0]
	instance.CompletedAtLocalTime = "23:30"
	// Europe/Berlin is UTC+1 on 3 February.
	instance.CompletedAtUtc = time.Date(2026, 2, 3, 22, 30, 0, 0, time.UTC)

	require.NoError(t, spec.Validate())
}

func TestCompletionInstant(t *testing.T) {
	t.Parallel()
	berlin, err := time.LoadLocation("Europe/Berlin")
	require.NoError(t, err)

	instance := Instance{CompletedOn: dateonly.New(2026, 2, 3)}
	got, ok := instance.CompletionInstant(berlin, "10:00")
	require.True(t, ok)
	assert.Equal(t, "2026-02-03T09:00:00Z", got.Format(time.RFC3339), "10:00 CET is 09:00Z")

	instance.CompletedAtLocalTime = "23:30"
	got, ok = instance.CompletionInstant(berlin, "10:00")
	require.True(t, ok)
	assert.Equal(t, "2026-02-03T22:30:00Z", got.Format(time.RFC3339))

	// summer time: Europe/Berlin is UTC+2 in July
	summer := Instance{CompletedOn: dateonly.New(2026, 7, 3)}
	got, _ = summer.CompletionInstant(berlin, "10:00")
	assert.Equal(t, "2026-07-03T08:00:00Z", got.Format(time.RFC3339))

	_, ok = Instance{}.CompletionInstant(berlin, "10:00")
	assert.False(t, ok, "an instance with no completion has no instant")
	_, ok = instance.CompletionInstant(nil, "10:00")
	assert.False(t, ok, "no zone, no instant")
	_, ok = Instance{CompletedOn: dateonly.New(2026, 2, 3), CompletedAtLocalTime: "nope"}.
		CompletionInstant(berlin, "10:00")
	assert.False(t, ok, "a malformed local time yields no instant")
}

func TestLocalCompletionTimeAndDefaults(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "10:00", Instance{}.LocalCompletionTime("10:00"))
	assert.Equal(t, DefaultCompletionLocalTime, Instance{}.LocalCompletionTime(""))
	assert.Equal(t, "07:30", Instance{CompletedAtLocalTime: "07:30"}.LocalCompletionTime("10:00"))

	assert.Equal(t, DefaultCompletionLocalTime, (&Spec{}).CompletionLocalTime())
	assert.Equal(t, "09:15",
		(&Spec{Conventions: Conventions{CompletionLocalTime: "09:15"}}).CompletionLocalTime())
}

func TestLookups(t *testing.T) {
	t.Parallel()
	spec := fixture()

	tenant, ok := spec.Tenant("acme")
	require.True(t, ok)
	_, ok = spec.Tenant("nope")
	assert.False(t, ok)

	assert.Len(t, tenant.AllUsers(), 4, "the admin counts as a user")

	admin, ok := tenant.User("acme.admin")
	require.True(t, ok)
	assert.Equal(t, "admin@acme.test", admin.Email)
	assert.True(t, admin.IsActive())

	retired, ok := tenant.User("acme.retired")
	require.True(t, ok)
	assert.False(t, retired.IsActive())

	entity, ok := tenant.Entity("acme.de")
	require.True(t, ok)
	assert.Equal(t, "Germany", entity.Country)

	obligation, ok := tenant.ObligationType("acme.vat")
	require.True(t, ok)
	assert.Equal(t, "VAT", obligation.Template)

	entityObligation, ok := tenant.EntityObligation("acme.de-vat")
	require.True(t, ok)
	assert.Equal(t, "monthly", entityObligation.Periodicity)

	workflow, ok := tenant.Workflow("acme.w1")
	require.True(t, ok)
	assert.False(t, workflow.IsProject())
	assert.Equal(t, []string{"M1", "M2"}, workflow.PeriodCodes())

	template, ok := workflow.Template("review")
	require.True(t, ok)
	assert.True(t, template.ApprovalRequired)
	_, ok = workflow.Template("nope")
	assert.False(t, ok)

	project, ok := tenant.Workflow("acme.w2")
	require.True(t, ok)
	assert.True(t, project.IsProject())
	assert.Equal(t, []string{ProjectPeriodCode}, project.PeriodCodes())
}
