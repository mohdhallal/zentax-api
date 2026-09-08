// Package domain holds the read models behind /reports: enriched task
// instances (instance + workflow + entity + obligation type + assignee name)
// and per-workflow completion stats. Read-only — nothing here mutates state.
package domain

import (
	"context"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// TaskInstanceRow is one enriched task instance: the task_instances row joined
// to its workflow, the workflow's entity and obligation type (both LEFT — a
// project workflow may have neither) and the assignee's display name (resolved
// at read time from the user row, ADR-0008). Date columns are legal date-only
// values (ADR-0002); instants are UTC (ADR-0003). tenant_id is RLS infra and
// deliberately absent.
type TaskInstanceRow struct {
	ID             string        `db:"id"`
	WorkflowID     string        `db:"workflow_id"`
	WorkflowTaskID string        `db:"workflow_task_id"`
	PeriodCode     string        `db:"period_code"`
	Name           string        `db:"name"`
	Description    *string       `db:"description"`
	TaskType       string        `db:"task_type"`
	Status         string        `db:"status"`
	AssigneeID     *string       `db:"assignee_id"`
	AssigneeName   *string       `db:"assignee_name"`
	DueDate        dateonly.Date `db:"due_date"`
	PeriodEndDate  dateonly.Date `db:"period_end_date"`
	FilingDeadline dateonly.Date `db:"filing_deadline"`
	// PaymentDeadline (ADR-0023 §5) is NULL on instances generated before it existed.
	PaymentDeadline  *dateonly.Date `db:"payment_deadline"`
	ApprovalRequired bool           `db:"approval_required"`
	ApprovedBy       *string        `db:"approved_by"`
	ApprovedAt       *time.Time     `db:"approved_at"`
	CompletedAt      *time.Time     `db:"completed_at"`
	SubmittedBy      *string        `db:"submitted_by"`
	SubmittedAt      *time.Time     `db:"submitted_at"`
	RejectionReason  *string        `db:"rejection_reason"`
	OrderIndex       int            `db:"order_index"`
	Notes            *string        `db:"notes"`
	DataTemplateID   *string        `db:"data_template_id"`
	TaxDataStatus    string         `db:"tax_data_status"`
	CreatedAt        time.Time      `db:"created_at"`
	UpdatedAt        time.Time      `db:"updated_at"`

	WorkflowName       string  `db:"workflow_name"`
	WorkflowCategory   string  `db:"workflow_category"`
	ProjectType        *string `db:"project_type"`
	FinancialYear      *string `db:"financial_year"`
	EntityID           *string `db:"entity_id"`
	EntityName         *string `db:"entity_name"`
	ObligationTypeID   *string `db:"obligation_type_id"`
	ObligationTypeName *string `db:"obligation_type_name"`
	TaxType            *string `db:"tax_type"` // obligation_types.template (VAT/CIT/...)
}

// Sort columns accepted by ListTaskInstances. Kept as an allow-list so the
// repository never interpolates a caller-controlled column name.
const (
	SortByDueDate   = "due_date"
	SortByCreatedAt = "created_at"
)

// Pseudo-values a task filter accepts on top of the stored ones.
const (
	// StatusOpen selects every instance that is not completed — the "open
	// work" the dashboard and the tasks page reason about.
	StatusOpen = "open"
	// FinancialYearNone selects instances whose workflow carries no financial
	// year (workflows.financial_year IS NULL), so project workflows can always
	// be kept inside a year scope.
	FinancialYearNone = "none"
)

// TaskFilters are the filters the task feed and the task summary share. nil /
// empty means "any". Status is a stored status or StatusOpen; FinancialYears is
// a set (OR-ed) that may contain FinancialYearNone. The repository assembles a
// predicate ONLY for the filters that are set, so the statement shape (and
// the planner's use of the indexes) follows the request rather than a generic
// `($n IS NULL OR …)` plan.
type TaskFilters struct {
	WorkflowID       *string
	EntityID         *string
	AssigneeID       *string
	FinancialYears   []string
	Status           *string
	WorkflowCategory *string
}

// ListTaskInstancesArgs are the validated filters + paging for the enriched
// task-instance list. SortColumn must be one of the Sort* constants (the
// repository falls back to due_date otherwise).
type ListTaskInstancesArgs struct {
	TaskFilters
	SortColumn string
	SortDesc   bool
	Limit      int
	Offset     int
}

// Task-instance statuses, as stored. StatusRank orders them the way the task
// board reads (open work first, completed, then blocked).
const (
	StatusNotStarted      = "not_started"
	StatusInProgress      = "in_progress"
	StatusInReview        = "in_review"
	StatusPendingApproval = "pending_approval"
	StatusCompleted       = "completed"
	StatusBlocked         = "blocked"
)

// TaskStatuses is every stored status, in rank order — the byStatus keys the
// summary always carries.
var TaskStatuses = []string{
	StatusNotStarted, StatusInProgress, StatusInReview, StatusPendingApproval, StatusCompleted, StatusBlocked,
}

// TaskSummary is the exact tile set of the dashboard / tasks page over the
// filtered instance set, computed in ONE aggregate statement against the
// tenant's civil "today" (ADR-0023 §6): Overdue / DueToday / DueThisWeek count
// open instances only (a completed instance is never bucketed), and the week
// ends on Saturday. Today is that civil date (zero only when the tenant
// registry row is missing, which an authenticated request cannot reach).
type TaskSummary struct {
	Today            dateonly.Date `db:"today"`
	Total            int           `db:"total"`
	Completed        int           `db:"completed"`
	Active           int           `db:"active"`
	Overdue          int           `db:"overdue"`
	DueToday         int           `db:"due_today"`
	DueThisWeek      int           `db:"due_this_week"`
	AwaitingApproval int           `db:"awaiting_approval"`
	NotStarted       int           `db:"not_started"`
	InProgress       int           `db:"in_progress"`
	InReview         int           `db:"in_review"`
	PendingApproval  int           `db:"pending_approval"`
	Blocked          int           `db:"blocked"`
}

// ByStatus is the count per stored status, every key present (0 when none).
func (s TaskSummary) ByStatus() map[string]int {
	return map[string]int{
		StatusNotStarted:      s.NotStarted,
		StatusInProgress:      s.InProgress,
		StatusInReview:        s.InReview,
		StatusPendingApproval: s.PendingApproval,
		StatusCompleted:       s.Completed,
		StatusBlocked:         s.Blocked,
	}
}

// CompletionRate is Completed/Total as a rounded integer percentage — the same
// rounding as WorkflowStats.CompletionPercent — and 0 when there is nothing.
func (s TaskSummary) CompletionRate() int {
	return roundedPercent(s.Completed, s.Total)
}

// WorkflowStats is the per-workflow completion summary. NextDueDate is the
// earliest due date among the workflow's not-yet-completed instances (zero when
// there is none — rendered as null).
type WorkflowStats struct {
	WorkflowID     string        `db:"workflow_id"`
	TotalTasks     int           `db:"total_tasks"`
	CompletedTasks int           `db:"completed_tasks"`
	NextDueDate    dateonly.Date `db:"next_due_date"`
}

// CompletionPercent is completed/total rounded to the nearest integer, 0 when
// the workflow has no instances.
func (s WorkflowStats) CompletionPercent() int {
	return roundedPercent(s.CompletedTasks, s.TotalTasks)
}

// roundedPercent is part/total × 100 rounded half-up to an integer, 0 when
// total is 0. Integer arithmetic only, so it cannot drift: (2·p·100 + t) / 2t.
func roundedPercent(part, total int) int {
	if total <= 0 {
		return 0
	}
	return (2*part*100 + total) / (2 * total)
}

// ---------------------------------------------------------------------------
// Compliance / financial reports (ADR-0021: SQL aggregation over the tenant's
// own partitions; capped row lists with exact totals).
// ---------------------------------------------------------------------------

// Row-list caps shared by every report that returns rows (ADR-0021 rule 2).
const (
	DefaultReportLimit = 1000
	MaxReportLimit     = 5000
)

// ReportFilters are the workflow-level filters every compliance / financial
// report accepts. nil means "any". FinancialYear matches workflows.financial_year
// exactly ("2025"); the ids match workflows.entity_id / obligation_type_id.
type ReportFilters struct {
	FinancialYear    *string
	EntityID         *string
	ObligationTypeID *string
}

// Heatmap view modes: columns are period codes or obligation types.
const (
	ViewModePeriod  = "period"
	ViewModeTaxType = "tax-type"
)

// Cell / instance classifications, as the SQL yields them.
const (
	ComplianceOnTime = "on_time"
	ComplianceLate   = "late"
	ComplianceMissed = "missed"
	ComplianceNotDue = "not_due"

	CellGreen = "green"
	CellAmber = "amber"
	CellRed   = "red"
	CellGrey  = "grey"
)

type HeatmapArgs struct {
	ReportFilters
	ViewMode string // ViewModePeriod | ViewModeTaxType
}

// HeatmapCell is one (entity × period|obligation-type) aggregate: counts by
// status plus the distinct workflow ids feeding the cell (comma-joined by the
// SQL string_agg; see WorkflowIDList). Cells only exist where instances exist,
// so TotalTasks is never 0 in practice — Status still handles it.
type HeatmapCell struct {
	RowID           string `db:"row_id"`
	RowLabel        string `db:"row_label"`
	ColID           string `db:"col_id"`
	ColLabel        string `db:"col_label"`
	TotalTasks      int    `db:"total_tasks"`
	CompletedTasks  int    `db:"completed_tasks"`
	OverdueTasks    int    `db:"overdue_tasks"`
	CompletedLate   int    `db:"completed_late"`
	InProgressTasks int    `db:"in_progress_tasks"`
	WorkflowIDs     string `db:"workflow_ids"`
	// FirstPeriodEnd orders columns by the calendar (M2 before M10), not by
	// period-code text; it is never serialized.
	FirstPeriodEnd time.Time `db:"first_period_end"`
}

// Status is the legacy traffic-light rule: no tasks → grey; anything overdue
// or completed late → red; everything completed → green; otherwise amber.
func (c HeatmapCell) Status() string {
	switch {
	case c.TotalTasks == 0:
		return CellGrey
	case c.OverdueTasks > 0 || c.CompletedLate > 0:
		return CellRed
	case c.CompletedTasks == c.TotalTasks:
		return CellGreen
	default:
		return CellAmber
	}
}

// WorkflowIDList splits the aggregated ids; never nil.
func (c HeatmapCell) WorkflowIDList() []string {
	if c.WorkflowIDs == "" {
		return []string{}
	}
	return strings.Split(c.WorkflowIDs, ",")
}

type ComplianceStatusArgs struct {
	ReportFilters
	Status *string // Compliance* constant; nil = any
	Limit  int
	Offset int
}

// ComplianceRow is one task instance classified against its filing deadline.
// FilingDeadline is date-only (ADR-0002); CompletedAt is the filing instant
// (UTC, ADR-0003) when the instance is completed.
type ComplianceRow struct {
	EntityName       string        `db:"entity_name"`
	EntityID         string        `db:"entity_id"`
	TaxType          string        `db:"tax_type"`
	ObligationName   string        `db:"obligation_name"`
	ObligationCode   string        `db:"obligation_code"`
	Period           string        `db:"period"`
	FilingDeadline   dateonly.Date `db:"filing_deadline"`
	CompletedAt      *time.Time    `db:"completed_at"`
	ComplianceStatus string        `db:"compliance_status"`
	PenaltyInterest  string        `db:"penalty_interest"`
	WorkflowID       string        `db:"workflow_id"`
	TaskInstanceID   string        `db:"task_instance_id"`
}

// ComplianceSummary counts the classified set BEFORE the status filter (the
// year / entity / obligation filters still apply): the cards describe the
// population the rows page was cut from, so Total = OnTime + Late + Missed +
// NotDue whatever status the page is narrowed to.
type ComplianceSummary struct {
	Total  int `db:"total"`
	OnTime int `db:"on_time"`
	Late   int `db:"late"`
	Missed int `db:"missed"`
	NotDue int `db:"not_due"`
}

// ComplianceStatusResult: Rows is a capped page of the status-filtered set and
// TotalCount its exact size; Summary is exact over the unfiltered-by-status set.
type ComplianceStatusResult struct {
	Rows       []ComplianceRow
	Summary    ComplianceSummary
	TotalCount int
}

// Tax-financial grouping keys.
const (
	GroupByEntity     = "entity"
	GroupByCountry    = "country"
	GroupByTaxType    = "taxType"
	GroupByPeriod     = "period"
	GroupByObligation = "obligation"
)

type TaxFinancialArgs struct {
	ReportFilters
	GroupBy string // GroupBy* constant
	Limit   int
	Offset  int
}

// Figures are the fixed set of financial amounts extracted from tax_data
// (ADR-0021 rule 6: bounded key set, safe numeric cast — never a string).
type Figures struct {
	OutputVat      float64 `db:"output_vat"`
	InputVat       float64 `db:"input_vat"`
	NetVat         float64 `db:"net_vat"`
	TaxableIncome  float64 `db:"taxable_income"`
	TaxLiability   float64 `db:"tax_liability"`
	WhtAmount      float64 `db:"wht_amount"`
	EngagementCost float64 `db:"engagement_cost"`
	TotalAmount    float64 `db:"total_amount"`
}

// FinancialRow is one instance with tax data, with its figures.
type FinancialRow struct {
	EntityName     string `db:"entity_name"`
	EntityID       string `db:"entity_id"`
	Country        string `db:"country"`
	TaxType        string `db:"tax_type"`
	ObligationName string `db:"obligation_name"`
	ObligationCode string `db:"obligation_code"`
	Period         string `db:"period"`
	FinancialYear  string `db:"financial_year"`
	Figures
}

// FinancialGroup is one GROUP BY bucket of the requested groupBy.
type FinancialGroup struct {
	Key   string `db:"group_key"`
	Label string `db:"group_label"`
	Figures
	Count int `db:"cnt"`
}

// FinancialPeriodPoint is one chart point: the figures summed per period.
type FinancialPeriodPoint struct {
	Period string `db:"period"`
	Figures
}

type FinancialSummary struct {
	Figures
	RecordCount int
}

// TaxFinancialResult: Rows is a capped page; Aggregated / ChartData / Summary
// are exact aggregates over the whole filtered set; TotalCount is exact.
type TaxFinancialResult struct {
	Rows       []FinancialRow
	Aggregated []FinancialGroup
	ChartData  []FinancialPeriodPoint
	Summary    FinancialSummary
	TotalCount int
}

// Export datasets.
const (
	DatasetWorkflows = "workflows"
	DatasetTasks     = "tasks"
	DatasetTaxData   = "tax-data"
)

// ExportArgs: Category filters workflows.workflow_category; the date window
// (inclusive, date-only) applies to workflows.created_at (UTC date) for the
// workflows dataset and to the instance's due_date for tasks / tax-data —
// mirroring the legacy export. Every workflow status takes part.
type ExportArgs struct {
	EntityID         *string
	ObligationTypeID *string
	Category         *string
	DateFrom         *dateonly.Date
	DateTo           *dateonly.Date
	Limit            int
	Offset           int
}

type ExportWorkflowRow struct {
	Name            string    `db:"name"`
	Category        string    `db:"category"`
	ProjectType     *string   `db:"project_type"`
	FinancialYear   *string   `db:"financial_year"`
	Periodicity     *string   `db:"periodicity"`
	EntityName      *string   `db:"entity_name"`
	Country         *string   `db:"country"`
	ObligationName  *string   `db:"obligation_name"`
	ObligationCode  *string   `db:"obligation_code"`
	TaxType         *string   `db:"tax_type"`
	Status          string    `db:"status"`
	StartDate       *string   `db:"start_date"`
	EndDate         *string   `db:"end_date"`
	TasksSequential bool      `db:"tasks_sequential"`
	CreatedAt       time.Time `db:"created_at"`
}

type ExportTaskRow struct {
	Name             string        `db:"name"`
	TaskType         string        `db:"task_type"`
	Status           string        `db:"status"`
	WorkflowName     string        `db:"workflow_name"`
	WorkflowCategory string        `db:"workflow_category"`
	EntityName       *string       `db:"entity_name"`
	Country          *string       `db:"country"`
	ObligationName   *string       `db:"obligation_name"`
	TaxType          *string       `db:"tax_type"`
	PeriodCode       string        `db:"period_code"`
	FinancialYear    *string       `db:"financial_year"`
	AssigneeName     *string       `db:"assignee_name"`
	DueDate          dateonly.Date `db:"due_date"`
	FilingDeadline   dateonly.Date `db:"filing_deadline"`
	CompletedAt      *time.Time    `db:"completed_at"`
	ApprovalRequired bool          `db:"approval_required"`
	TaxDataStatus    string        `db:"tax_data_status"`
}

type ExportTaxDataRow struct {
	Name           string  `db:"name"`
	WorkflowName   string  `db:"workflow_name"`
	EntityName     *string `db:"entity_name"`
	Country        *string `db:"country"`
	ObligationName *string `db:"obligation_name"`
	TaxType        *string `db:"tax_type"`
	PeriodCode     string  `db:"period_code"`
	FinancialYear  *string `db:"financial_year"`
	TaxDataStatus  string  `db:"tax_data_status"`
	OutputVat      float64 `db:"output_vat"`
	InputVat       float64 `db:"input_vat"`
	NetVat         float64 `db:"net_vat"`
	TaxableIncome  float64 `db:"taxable_income"`
	TaxLiability   float64 `db:"tax_liability"`
	WhtAmount      float64 `db:"wht_amount"`
	PenaltyAmount  float64 `db:"penalty_amount"`
	InterestAmount float64 `db:"interest_amount"`
	EngagementCost float64 `db:"engagement_cost"`
}

// Reader is the read-side port implemented by the Postgres repository. Every
// query runs on the request transaction, so RLS confines it to the session
// tenant (ADR-0004) — implementations never filter by tenant_id themselves.
type Reader interface {
	// ListTaskInstances returns one page of enriched instances plus the total
	// number of rows matching the filters.
	ListTaskInstances(ctx context.Context, args ListTaskInstancesArgs) ([]TaskInstanceRow, int, error)
	// TaskSummary returns the exact tile counts over the instances matching
	// the filters — one aggregate statement, never a row walk.
	TaskSummary(ctx context.Context, filters TaskFilters) (*TaskSummary, error)
	// WorkflowStats returns one entry per workflow in the tenant, including
	// workflows with no instances yet.
	WorkflowStats(ctx context.Context) ([]WorkflowStats, error)

	// ComplianceHeatmap returns one aggregate cell per (entity, column), sorted
	// by (row label, column id) — one GROUP BY statement.
	ComplianceHeatmap(ctx context.Context, args HeatmapArgs) ([]HeatmapCell, error)
	// ComplianceStatus returns one page of classified instances (status-filtered,
	// with its exact total) plus the summary of the whole classified set.
	ComplianceStatus(ctx context.Context, args ComplianceStatusArgs) (*ComplianceStatusResult, error)
	// TaxFinancial returns a page of figure rows plus exact aggregates.
	TaxFinancial(ctx context.Context, args TaxFinancialArgs) (*TaxFinancialResult, error)
	// Export* return one page of the dataset plus its exact total.
	ExportWorkflows(ctx context.Context, args ExportArgs) ([]ExportWorkflowRow, int, error)
	ExportTasks(ctx context.Context, args ExportArgs) ([]ExportTaskRow, int, error)
	ExportTaxData(ctx context.Context, args ExportArgs) ([]ExportTaxDataRow, int, error)
}
