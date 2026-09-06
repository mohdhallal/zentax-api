// Package spec is the typed reading of seed/demo/dataset.json — the demo
// dataset's machine-readable contract.
//
// It carries data only: the tenants to create, the workflow chain to build, the
// per-instance deviations to apply, and the numbers each report must return.
// It deliberately contains NO report or oracle logic: how a heatmap cell or a
// compliance class is recomputed belongs to the verifier, not to the loader.
//
// The decoder is strict (see Load): a key the structs do not know is an error,
// so a dataset that grows a field fails loudly here instead of being silently
// half-seeded.
package spec

import (
	"encoding/json"
	"time"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// ProjectPeriodCode is the period code a started project workflow's single
// generated instance carries (product fix F6). Project workflows declare no
// selectedPeriods, so their instances and documents reference this instead.
const ProjectPeriodCode = "PROJECT"

// Spec is one dataset.json.
type Spec struct {
	// SchemaNotes is the "$schemaNotes" object: prose documenting every field.
	// Kept raw so the notes can be rewritten without touching this package.
	SchemaNotes json.RawMessage `json:"$schemaNotes"`

	// AsOf is the civil date the ExpectedAsOf tables were computed for.
	AsOf dateonly.Date `json:"asOf"`
	// ValidityWindow is the range of tenant-"today" values for which the
	// classification-based expectations still hold.
	ValidityWindow ValidityWindow `json:"validityWindow"`
	// Conventions documents the demo-wide rules (completion time, e-mail
	// pattern, password policy).
	Conventions Conventions `json:"conventions"`
	// ProductFixes lists the product fixes the dataset depends on ("F1", …).
	ProductFixes []string `json:"productFixes"`
	// Tenants is everything to create, in order.
	Tenants []Tenant `json:"tenants"`
	// ExpectedAsOf is keyed by civil date, then by tenant key.
	ExpectedAsOf map[string]map[string]Expectations `json:"expectedAsOf"`
}

type ValidityWindow struct {
	From dateonly.Date `json:"from"`
	To   dateonly.Date `json:"to"`
	Note string        `json:"note"`
}

type Conventions struct {
	CompletionLocalTime string `json:"completionLocalTime"`
	EmailPattern        string `json:"emailPattern"`
	PasswordPolicy      string `json:"passwordPolicy"`
}

// Tenant is one demo tenant and everything inside it. Key is the stable handle
// cross-references use ("acme"); Slug is what lands in tenants.slug.
type Tenant struct {
	Key               string             `json:"key"`
	Slug              string             `json:"slug"`
	Name              string             `json:"name"`
	Timezone          string             `json:"timezone"`
	Admin             User               `json:"admin"`
	Users             []User             `json:"users"`
	Entities          []Entity           `json:"entities"`
	ObligationTypes   []ObligationType   `json:"obligationTypes"`
	EntityObligations []EntityObligation `json:"entityObligations"`
	Workflows         []Workflow         `json:"workflows"`
}

// User is a member of a tenant. The admin is created with the tenant (SQL,
// like cmd/seed-admin); everyone else is invited, accepts, then logs in.
type User struct {
	Key   string `json:"key"`
	Email string `json:"email"`
	Name  string `json:"name"`
	// Role is tenant_admin | manager | preparer | reviewer | viewer.
	Role string `json:"role"`
	// ScopeEntity is nil for a tenant-wide grant, else the entity key whose
	// subtree the grant is scoped to (writes only; reads stay tenant-wide).
	ScopeEntity *string `json:"scopeEntity"`
	Password    string  `json:"password"`
	// Status is active | disabled. A disabled member is created active, used,
	// and disabled afterwards.
	Status string `json:"status"`
}

// IsActive reports whether the user can act (be a writer, approver, assignee).
func (u User) IsActive() bool { return u.Status == StatusActive }

type Entity struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	LegalName    string `json:"legalName"`
	Country      string `json:"country"`
	TaxResidency string `json:"taxResidency"`
	// Parent is the parent entity's key, or nil for a root.
	Parent                *string `json:"parent"`
	FiscalCalendarPattern string  `json:"fiscalCalendarPattern"`
	// FinancialYearEnd is MM-DD (a legal date-only value, ADR-0002).
	FinancialYearEnd string `json:"financialYearEnd"`
}

type ObligationType struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Code string `json:"code"`
	// Template is VAT | CIT | WHT | TP | Custom — it drives the report taxType.
	Template string `json:"template"`
	// Category is predefined | custom.
	Category    string `json:"category"`
	Description string `json:"description"`
}

// EntityObligation links an entity to an obligation type. Exactly one may exist
// per (entity, obligationType).
type EntityObligation struct {
	Key                string       `json:"key"`
	Entity             string       `json:"entity"`
	ObligationType     string       `json:"obligationType"`
	Periodicity        string       `json:"periodicity"`
	Currency           string       `json:"currency"`
	Jurisdiction       string       `json:"jurisdiction"`
	TaxReferenceNumber *string      `json:"taxReferenceNumber"`
	DeadlineRule       DeadlineRule `json:"deadlineRule"`
}

// DeadlineRule is the obligation's date configuration. Only PaymentOffset /
// PaymentFixedDates / WeekendAdjustment reach the generator (they produce the
// payment deadline); FilingOffset and FixedDates are stored for the UI.
type DeadlineRule struct {
	// Type is fixed | period_offset.
	Type string `json:"type"`
	// Reference is the anchor the offsets apply to (period_end).
	Reference string `json:"reference"`
	// WeekendAdjustment is none | next-business-day.
	WeekendAdjustment string    `json:"weekendAdjustment"`
	PeriodStart       *MonthDay `json:"periodStart,omitempty"`
	FilingOffset      *Offset   `json:"filingOffset,omitempty"`
	PaymentOffset     *Offset   `json:"paymentOffset,omitempty"`
	// FixedDates and PaymentFixedDates are MM-DD strings.
	FixedDates        []string `json:"fixedDates,omitempty"`
	PaymentFixedDates []string `json:"paymentFixedDates,omitempty"`
}

type MonthDay struct {
	Day   int `json:"day"`
	Month int `json:"month"`
}

type Offset struct {
	Months int `json:"months"`
	Days   int `json:"days"`
}

// Workflow is one workflow to create, with its task templates and the instance
// deviations to apply after it is started.
type Workflow struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Category is recurring | project.
	Category string `json:"category"`
	// ProjectType is set for project workflows only (dispute, audit_verification,
	// due_diligence, market_expansion, advisory, restructuring, custom).
	ProjectType *string `json:"projectType"`
	// Entity is the entity key; nil on a tenant-level project workflow.
	Entity *string `json:"entity"`
	// ObligationType and EntityObligation are set for recurring workflows only.
	ObligationType   *string `json:"obligationType"`
	EntityObligation *string `json:"entityObligation"`
	// FinancialYear is the calendar year the fiscal year ENDS in.
	FinancialYear string `json:"financialYear"`
	// Periodicity is monthly | quarterly | annual (recurring workflows only).
	Periodicity *string `json:"periodicity"`
	// SelectedPeriods are the period codes to generate instances for; empty on
	// a project workflow (its single period is ProjectPeriodCode).
	SelectedPeriods []string      `json:"selectedPeriods"`
	DueDateRule     DueDateRule   `json:"dueDateRule"`
	StartDate       dateonly.Date `json:"startDate"`
	EndDate         dateonly.Date `json:"endDate"`
	TasksSequential bool          `json:"tasksSequential"`
	// Writer is the user whose session performs the task-instance writes and
	// document uploads; Approver approves them (they must differ — SoD).
	Writer    string         `json:"writer"`
	Approver  string         `json:"approver"`
	Lifecycle Lifecycle      `json:"lifecycle"`
	Templates []TaskTemplate `json:"templates"`
	// Instances lists only the generated instances that deviate from the
	// default (not_started, unassigned, no data).
	Instances []Instance `json:"instances"`
	Documents []Document `json:"documents"`
}

// IsProject reports whether this is a one-off project workflow.
func (w Workflow) IsProject() bool { return w.Category == CategoryProject }

// PeriodCodes returns the period codes this workflow's instances may reference:
// the selected periods, or the single project period.
func (w Workflow) PeriodCodes() []string {
	if w.IsProject() {
		return []string{ProjectPeriodCode}
	}
	return w.SelectedPeriods
}

// DueDateRule is the workflow-level filing-deadline rule: filingDeadline =
// weekendAdjust(periodEnd ± offset).
type DueDateRule struct {
	Reference         string `json:"reference"`
	OffsetUnit        string `json:"offsetUnit"`
	OffsetValue       int    `json:"offsetValue"`
	OffsetDirection   string `json:"offsetDirection"`
	WeekendAdjustment string `json:"weekendAdjustment"`
}

// Lifecycle says whether the workflow is started, and the status it ends in.
type Lifecycle struct {
	Start bool `json:"start"`
	// FinalStatus is draft | active | completed | archived.
	FinalStatus string `json:"finalStatus"`
}

// TaskTemplate is one workflow task (the template instances are generated from).
type TaskTemplate struct {
	Key              string `json:"key"`
	Name             string `json:"name"`
	TaskType         string `json:"taskType"`
	RoleLabel        string `json:"roleLabel"`
	ApprovalRequired bool   `json:"approvalRequired"`
	// DueDateReference is period_end | filing_deadline | payment_deadline.
	DueDateReference       string `json:"dueDateReference"`
	DueDateOffsetValue     int    `json:"dueDateOffsetValue"`
	DueDateOffsetUnit      string `json:"dueDateOffsetUnit"`
	DueDateOffsetDirection string `json:"dueDateOffsetDirection"`
	OrderIndex             int    `json:"orderIndex"`
	// DataTemplate is VAT | CIT | WHT — the tenant's predefined data template
	// of that type — or nil for no template.
	DataTemplate *string `json:"dataTemplate"`
}

// Instance is one generated task instance that deviates from the default.
type Instance struct {
	// Period is a workflow period code (or ProjectPeriodCode).
	Period string `json:"period"`
	// Task is a TaskTemplate key of the same workflow.
	Task string `json:"task"`
	// Status is not_started | in_progress | in_review | blocked |
	// pending_approval | completed.
	Status string `json:"status"`
	// Via is how the state is reached: put | submit | approve.
	Via string `json:"via"`
	// Assignee is the user key set on the PUT; must be an active member.
	Assignee string `json:"assignee"`
	// CompletedOn is the civil completion date in the TENANT's timezone.
	CompletedOn dateonly.Date `json:"completedOn,omitempty"`
	// CompletedAtLocalTime overrides the default completion time (HH:MM).
	CompletedAtLocalTime string `json:"completedAtLocalTime,omitempty"`
	// CompletedAtUtc is the exact instant the completion escape hatch must
	// write, declared only where the tenant-zone civil date differs from the
	// UTC one. It equals CompletedOn at the local completion time read in the
	// tenant's zone; the verifier asserts the stored instant, not just the
	// derived civil date. Zero when not declared.
	CompletedAtUtc time.Time `json:"completedAtUtc,omitempty"`
	// SubmittedOn is the civil submission date (via=approve).
	SubmittedOn dateonly.Date `json:"submittedOn,omitempty"`
	// TaxData is the taxData object stored by the PUT. With a data template
	// attached, its keys are the template's field ids (product fix F1).
	TaxData map[string]any `json:"taxData,omitempty"`
	// TaxDataStatus is draft | final.
	TaxDataStatus string `json:"taxDataStatus,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

// Document is one document uploaded against a task instance.
type Document struct {
	Period       string `json:"period"`
	Task         string `json:"task"`
	DocumentType string `json:"documentType"`
	Label        string `json:"label"`
	// Kind is pdf | text | csv — the seeder generates a tiny valid file of that
	// MIME type.
	Kind     string `json:"kind"`
	FileName string `json:"fileName"`
	// Category is compliance | project.
	Category string `json:"category"`
	// NewVersion additionally posts a second version of the same document.
	NewVersion bool    `json:"newVersion"`
	Notes      *string `json:"notes"`
}

// Expectations is what every report must return for one tenant on one date.
// The maps are keyed by the query string the report is called with, with entity
// / obligation-type KEYS standing in for the ids the seeder resolves them to.
type Expectations struct {
	ComplianceHeatmap map[string]HeatmapExpectation          `json:"complianceHeatmap"`
	ComplianceStatus  map[string]ComplianceStatusExpectation `json:"complianceStatus"`
	TaxFinancial      map[string]TaxFinancialExpectation     `json:"taxFinancial"`
	// ExportRaw is the row count per export query.
	ExportRaw map[string]int `json:"exportRaw"`
	// WorkflowStats is keyed by workflow key.
	WorkflowStats map[string]WorkflowStatsExpectation `json:"workflowStats"`
	Dashboard     DashboardExpectation                `json:"dashboard"`
}

type HeatmapExpectation struct {
	Summary HeatmapSummary `json:"summary"`
	// ColOrder is the column order the report must return.
	ColOrder []string `json:"colOrder"`
	// Cells is keyed "<entityKey>|<colId>", colId being the period code
	// (period view) or the obligation-type key (tax-type view).
	Cells map[string]HeatmapCell `json:"cells"`
}

type HeatmapSummary struct {
	TotalCells int `json:"totalCells"`
	Green      int `json:"green"`
	Amber      int `json:"amber"`
	Red        int `json:"red"`
}

type HeatmapCell struct {
	TotalTasks      int    `json:"totalTasks"`
	CompletedTasks  int    `json:"completedTasks"`
	OverdueTasks    int    `json:"overdueTasks"`
	CompletedLate   int    `json:"completedLate"`
	InProgressTasks int    `json:"inProgressTasks"`
	Status          string `json:"status"`
}

type ComplianceStatusExpectation struct {
	Summary    ComplianceStatusSummary `json:"summary"`
	TotalCount int                     `json:"totalCount"`
	// RowsReturnedAtDefaultLimit is how many rows come back without paging.
	RowsReturnedAtDefaultLimit int `json:"rowsReturnedAtDefaultLimit"`
}

type ComplianceStatusSummary struct {
	Total  int `json:"total"`
	OnTime int `json:"onTime"`
	Late   int `json:"late"`
	Missed int `json:"missed"`
	NotDue int `json:"notDue"`
}

// TaxFinancialExpectation holds money as json.Number so the exact literal from
// the dataset survives — no float rounding on the way to a comparison.
type TaxFinancialExpectation struct {
	Summary             TaxFinancialSummary              `json:"summary"`
	TotalCount          int                              `json:"totalCount"`
	AggregatedByTaxType map[string]TaxFinancialAggregate `json:"aggregatedByTaxType"`
	AggregatedByEntity  map[string]TaxFinancialAggregate `json:"aggregatedByEntity"`
	AggregatedByCountry map[string]TaxFinancialAggregate `json:"aggregatedByCountry"`
	AggregatedByPeriod  map[string]TaxFinancialAggregate `json:"aggregatedByPeriod"`
	// ChartPeriodOrder is the period order the chart must render (product fix F4).
	ChartPeriodOrder []string `json:"chartPeriodOrder"`
}

type TaxFinancialSummary struct {
	TotalOutputVat      json.Number `json:"totalOutputVat"`
	TotalInputVat       json.Number `json:"totalInputVat"`
	TotalNetVat         json.Number `json:"totalNetVat"`
	TotalTaxableIncome  json.Number `json:"totalTaxableIncome"`
	TotalTaxLiability   json.Number `json:"totalTaxLiability"`
	TotalWht            json.Number `json:"totalWht"`
	TotalEngagementCost json.Number `json:"totalEngagementCost"`
	TotalAmount         json.Number `json:"totalAmount"`
	RecordCount         int         `json:"recordCount"`
}

type TaxFinancialAggregate struct {
	OutputVat      json.Number `json:"outputVat"`
	InputVat       json.Number `json:"inputVat"`
	NetVat         json.Number `json:"netVat"`
	TaxableIncome  json.Number `json:"taxableIncome"`
	TaxLiability   json.Number `json:"taxLiability"`
	WhtAmount      json.Number `json:"whtAmount"`
	EngagementCost json.Number `json:"engagementCost"`
	TotalAmount    json.Number `json:"totalAmount"`
	Count          int         `json:"count"`
}

type WorkflowStatsExpectation struct {
	TotalTasks        int `json:"totalTasks"`
	CompletedTasks    int `json:"completedTasks"`
	CompletionPercent int `json:"completionPercent"`
	// NextDueDate is zero when the workflow has no next due date (JSON null).
	NextDueDate dateonly.Date `json:"nextDueDate"`
}

type DashboardExpectation struct {
	Today            dateonly.Date `json:"today"`
	WeekEnd          dateonly.Date `json:"weekEnd"`
	TotalInstances   int           `json:"totalInstances"`
	Overdue          int           `json:"overdue"`
	DueToday         int           `json:"dueToday"`
	ThisWeek         int           `json:"thisWeek"`
	Active           int           `json:"active"`
	Completed        int           `json:"completed"`
	AwaitingApproval int           `json:"awaitingApproval"`
	CompletionRate   int           `json:"completionRate"`
	// ByStatus is keyed by task-instance status.
	ByStatus map[string]int `json:"byStatus"`
}
