package main

// The live side of verify: the API calls it makes, the payload shapes it
// decodes, and how a dataset key ("acme.de", "acme.w1") is resolved to the
// server id the API speaks in.
//
// Resolution deliberately does NOT depend on the seeder's key → id file:
// verify must run against a database seeded by any previous run (the test
// plan runs it with no such file at all). Keys are resolved from the live
// tenant by natural key — entity name, obligation-type code, workflow name —
// all of which the spec validates as unique per tenant. The seeder's file,
// when it is on disk, is only cross-checked against that resolution.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/mohamadhallal/zentax-api/shared/apiclient"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// verifyListPageSize is the largest page the list endpoints accept (100).
const verifyListPageSize = 100

// verifyInstancePageSize is the largest page /reports/task-instances accepts.
const verifyInstancePageSize = 500

// ---------------------------------------------------------------------------
// Payload shapes
// ---------------------------------------------------------------------------

type verifyEntityRow struct {
	ID                    string  `json:"id"`
	Name                  string  `json:"name"`
	Country               string  `json:"country"`
	FinancialYearEnd      *string `json:"financialYearEnd"`
	FiscalCalendarPattern string  `json:"fiscalCalendarPattern"`
	ParentEntityID        *string `json:"parentEntityId"`
}

type verifyObligationTypeRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Code     string `json:"code"`
	Template string `json:"template"`
	Category string `json:"category"`
}

type verifyWorkflowRow struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	WorkflowCategory string   `json:"workflowCategory"`
	Status           string   `json:"status"`
	FinancialYear    *string  `json:"financialYear"`
	Periodicity      *string  `json:"periodicity"`
	SelectedPeriods  []string `json:"selectedPeriods"`
	EntityID         *string  `json:"entityId"`
	ObligationTypeID *string  `json:"obligationTypeId"`
	StartDate        *string  `json:"startDate"`
	EndDate          *string  `json:"endDate"`
	CreatedAt        string   `json:"createdAt"`
}

type verifyInstanceRow struct {
	ID               string         `json:"id"`
	WorkflowID       string         `json:"workflowId"`
	PeriodCode       string         `json:"periodCode"`
	Name             string         `json:"name"`
	TaskType         string         `json:"taskType"`
	Status           string         `json:"status"`
	AssigneeID       *string        `json:"assigneeId"`
	AssigneeName     *string        `json:"assigneeName"`
	DueDate          dateonly.Date  `json:"dueDate"`
	PeriodEndDate    dateonly.Date  `json:"periodEndDate"`
	FilingDeadline   dateonly.Date  `json:"filingDeadline"`
	PaymentDeadline  *dateonly.Date `json:"paymentDeadline"`
	ApprovalRequired bool           `json:"approvalRequired"`
	ApprovedBy       *string        `json:"approvedBy"`
	ApprovedAt       *time.Time     `json:"approvedAt"`
	CompletedAt      *time.Time     `json:"completedAt"`
	SubmittedBy      *string        `json:"submittedBy"`
	SubmittedAt      *time.Time     `json:"submittedAt"`
	OrderIndex       int            `json:"orderIndex"`
	Notes            *string        `json:"notes"`
	DataTemplateID   *string        `json:"dataTemplateId"`
	TaxDataStatus    string         `json:"taxDataStatus"`
	WorkflowCategory string         `json:"workflowCategory"`
	EntityID         *string        `json:"entityId"`
	ObligationTypeID *string        `json:"obligationTypeId"`
}

type verifyPreview struct {
	WorkflowID    string              `json:"workflowId"`
	WorkflowName  string              `json:"workflowName"`
	TotalPeriods  int                 `json:"totalPeriods"`
	TaskTemplates int                 `json:"taskTemplates"`
	TotalTasks    int                 `json:"totalTasks"`
	Tasks         []verifyPreviewTask `json:"tasks"`
}

type verifyPreviewTask struct {
	TemplateID      string         `json:"templateId"`
	PeriodCode      string         `json:"periodCode"`
	Name            string         `json:"name"`
	TaskType        string         `json:"taskType"`
	DueDate         dateonly.Date  `json:"dueDate"`
	PeriodEndDate   dateonly.Date  `json:"periodEndDate"`
	FilingDeadline  dateonly.Date  `json:"filingDeadline"`
	PaymentDeadline *dateonly.Date `json:"paymentDeadline"`
	OrderIndex      int            `json:"orderIndex"`
}

type verifyIDLabel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type verifyHeatmapPayload struct {
	Rows    []verifyIDLabel     `json:"rows"`
	Cols    []verifyIDLabel     `json:"cols"`
	Cells   []verifyHeatmapCell `json:"cells"`
	Summary struct {
		TotalCells int `json:"totalCells"`
		Green      int `json:"green"`
		Amber      int `json:"amber"`
		Red        int `json:"red"`
	} `json:"summary"`
}

// verifyHeatmapCell has no completedLate: the payload does not carry it (the
// colour does). The oracle still counts it — it decides red.
type verifyHeatmapCell struct {
	RowID           string   `json:"rowId"`
	RowLabel        string   `json:"rowLabel"`
	ColID           string   `json:"colId"`
	ColLabel        string   `json:"colLabel"`
	Status          string   `json:"status"`
	TotalTasks      int      `json:"totalTasks"`
	CompletedTasks  int      `json:"completedTasks"`
	OverdueTasks    int      `json:"overdueTasks"`
	InProgressTasks int      `json:"inProgressTasks"`
	WorkflowIDs     []string `json:"workflowIds"`
}

type verifyCompliancePayload struct {
	Rows    []verifyComplianceRow `json:"rows"`
	Summary struct {
		Total  int `json:"total"`
		OnTime int `json:"onTime"`
		Late   int `json:"late"`
		Missed int `json:"missed"`
		NotDue int `json:"notDue"`
	} `json:"summary"`
	TotalCount int `json:"totalCount"`
}

type verifyComplianceRow struct {
	EntityName       string        `json:"entityName"`
	EntityID         string        `json:"entityId"`
	TaxType          string        `json:"taxType"`
	ObligationName   string        `json:"obligationName"`
	ObligationCode   string        `json:"obligationCode"`
	Period           string        `json:"period"`
	FilingDeadline   dateonly.Date `json:"filingDeadline"`
	FilingDate       *time.Time    `json:"filingDate"`
	ComplianceStatus string        `json:"complianceStatus"`
	PenaltyInterest  string        `json:"penaltyInterest"`
	WorkflowID       string        `json:"workflowId"`
	TaskInstanceID   string        `json:"taskInstanceId"`
}

// verifyFinancialFigures decodes the money columns as json.Number so a
// comparison never goes through a second float conversion.
type verifyFinancialFigures struct {
	OutputVat      json.Number `json:"outputVat"`
	InputVat       json.Number `json:"inputVat"`
	NetVat         json.Number `json:"netVat"`
	TaxableIncome  json.Number `json:"taxableIncome"`
	TaxLiability   json.Number `json:"taxLiability"`
	WhtAmount      json.Number `json:"whtAmount"`
	EngagementCost json.Number `json:"engagementCost"`
	TotalAmount    json.Number `json:"totalAmount"`
}

func (f verifyFinancialFigures) field(name string) json.Number {
	switch name {
	case "outputVat":
		return f.OutputVat
	case "inputVat":
		return f.InputVat
	case "netVat":
		return f.NetVat
	case "taxableIncome":
		return f.TaxableIncome
	case "taxLiability":
		return f.TaxLiability
	case "whtAmount":
		return f.WhtAmount
	case "engagementCost":
		return f.EngagementCost
	case "totalAmount":
		return f.TotalAmount
	}
	return ""
}

type verifyFinancialPayload struct {
	Rows       []verifyFinancialRow   `json:"rows"`
	Aggregated []verifyFinancialGroup `json:"aggregated"`
	ChartData  []verifyChartPoint     `json:"chartData"`
	Summary    struct {
		TotalOutputVat      json.Number `json:"totalOutputVat"`
		TotalInputVat       json.Number `json:"totalInputVat"`
		TotalNetVat         json.Number `json:"totalNetVat"`
		TotalTaxableIncome  json.Number `json:"totalTaxableIncome"`
		TotalTaxLiability   json.Number `json:"totalTaxLiability"`
		TotalWht            json.Number `json:"totalWht"`
		TotalEngagementCost json.Number `json:"totalEngagementCost"`
		TotalAmount         json.Number `json:"totalAmount"`
		RecordCount         int         `json:"recordCount"`
	} `json:"summary"`
	TotalCount int `json:"totalCount"`
}

// summaryFigure maps a figure name to the summary's differently-spelled key.
func (p *verifyFinancialPayload) summaryFigure(name string) json.Number {
	switch name {
	case "outputVat":
		return p.Summary.TotalOutputVat
	case "inputVat":
		return p.Summary.TotalInputVat
	case "netVat":
		return p.Summary.TotalNetVat
	case "taxableIncome":
		return p.Summary.TotalTaxableIncome
	case "taxLiability":
		return p.Summary.TotalTaxLiability
	case "whtAmount":
		return p.Summary.TotalWht
	case "engagementCost":
		return p.Summary.TotalEngagementCost
	case "totalAmount":
		return p.Summary.TotalAmount
	}
	return ""
}

type verifyFinancialRow struct {
	EntityName     string `json:"entityName"`
	EntityID       string `json:"entityId"`
	Country        string `json:"country"`
	TaxType        string `json:"taxType"`
	ObligationName string `json:"obligationName"`
	ObligationCode string `json:"obligationCode"`
	Period         string `json:"period"`
	FinancialYear  string `json:"financialYear"`
	verifyFinancialFigures
}

type verifyFinancialGroup struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
	verifyFinancialFigures
}

// verifyChartPoint carries only the figures the chart renders.
type verifyChartPoint struct {
	Period       string      `json:"period"`
	OutputVat    json.Number `json:"outputVat"`
	InputVat     json.Number `json:"inputVat"`
	NetVat       json.Number `json:"netVat"`
	TaxLiability json.Number `json:"taxLiability"`
	WhtAmount    json.Number `json:"whtAmount"`
	TotalAmount  json.Number `json:"totalAmount"`
}

type verifyExportPayload struct {
	Dataset    string           `json:"dataset"`
	Rows       []map[string]any `json:"rows"`
	TotalCount int              `json:"totalCount"`
}

type verifyWorkflowStat struct {
	TotalTasks        int     `json:"totalTasks"`
	CompletedTasks    int     `json:"completedTasks"`
	CompletionPercent int     `json:"completionPercent"`
	NextDueDate       *string `json:"nextDueDate"`
}

// ---------------------------------------------------------------------------
// Calls
// ---------------------------------------------------------------------------

// verifyClient is one tenant's authenticated view of the API.
type verifyClient struct {
	api      *apiclient.Client
	identity *apiclient.Identity
}

// verifyLogin signs in as the tenant's admin and reads the tenant registry
// row (its IANA zone is the "day" every report classifies against).
func verifyLogin(ctx context.Context, base *apiclient.Client, email, password string) (*verifyClient, error) {
	session, err := base.Login(ctx, email, password)
	if err != nil {
		return nil, fmt.Errorf("login %s: %w", email, err)
	}
	if session.MFARequired {
		return nil, fmt.Errorf("login %s: the demo admin must not require MFA", email)
	}
	identity, err := session.Me(ctx)
	if err != nil {
		return nil, fmt.Errorf("GET /auth/me as %s: %w", email, err)
	}
	return &verifyClient{api: session.Client, identity: identity}, nil
}

// verifyQuery builds a path with a deterministic query string.
func verifyQuery(path string, params map[string]string) string {
	if len(params) == 0 {
		return path
	}
	values := url.Values{}
	for k, v := range params {
		if v == "" {
			continue
		}
		values.Set(k, v)
	}
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

// verifyListAll pages a list endpoint to exhaustion (limit=100, the maximum
// those endpoints accept).
func verifyListAll[T any](ctx context.Context, c *verifyClient, path string) ([]T, error) {
	out := []T{}
	for offset := 0; ; {
		var page []T
		p, err := c.api.GetPaginated(ctx, verifyQuery(path, map[string]string{
			"limit":  fmt.Sprint(verifyListPageSize),
			"offset": fmt.Sprint(offset),
		}), &page)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", path, err)
		}
		out = append(out, page...)
		offset += len(page)
		if len(page) == 0 || !p.Present || offset >= p.Total {
			return out, nil
		}
	}
}

// verifyInstances reads every enriched task instance of the tenant — at 10⁵
// instances that is ~200 pages, so the walk narrates itself.
func verifyInstances(ctx context.Context, c *verifyClient, progress *verifyProgress) ([]verifyInstanceRow, error) {
	out := []verifyInstanceRow{}
	for offset, pageNo := 0, 0; ; pageNo++ {
		var page []verifyInstanceRow
		p, err := c.api.GetPaginated(ctx, verifyQuery("/reports/task-instances", map[string]string{
			"limit":  fmt.Sprint(verifyInstancePageSize),
			"offset": fmt.Sprint(offset),
			"sort":   "dueDate:asc",
		}), &page)
		if err != nil {
			return nil, fmt.Errorf("GET /reports/task-instances: %w", err)
		}
		out = append(out, page...)
		offset += len(page)
		if len(page) == 0 || !p.Present || offset >= p.Total {
			progress.step("task-instances: %d rows read", len(out))
			return out, nil
		}
		if pageNo%25 == 0 {
			progress.step("task-instances page %d/%d (%d/%d rows)",
				pageNo+1, (p.Total+verifyInstancePageSize-1)/verifyInstancePageSize, offset, p.Total)
		}
	}
}

// verifyReportPages walks a report endpoint's rows to exhaustion at the
// endpoints' page cap: page n is read at offset n × cap, and the walk stops
// when a page comes back empty or the offset reaches the totalCount the page
// reports. count returns one page's row count and its totalCount. The pages
// come back in order; the first one's summary describes the whole set.
//
// A tenant under the cap (every demo tenant) makes exactly one request, as
// before; the scale fixture's 96k compliance rows make twenty.
func verifyReportPages[P any](
	r *verifyRun, what, path string, params map[string]string, count func(*P) (rows, total int),
) ([]*P, error) {
	pages := []*P{}
	for offset := 0; ; {
		query := make(map[string]string, len(params)+2)
		for k, v := range params {
			query[k] = v
		}
		query["limit"] = strconv.Itoa(verifyReportLimit)
		query["offset"] = strconv.Itoa(offset)
		var page P
		full := verifyQuery(path, query)
		if err := r.client.api.GET(r.ctx, full, nil, &page); err != nil {
			return nil, fmt.Errorf("GET %s: %w", full, err)
		}
		pages = append(pages, &page)
		n, total := count(&page)
		offset += n
		if pageCount := (total + verifyReportLimit - 1) / verifyReportLimit; pageCount > 1 {
			r.progress.step("%s page %d/%d", what, len(pages), pageCount)
		}
		if n == 0 || offset >= total {
			return pages, nil
		}
	}
}

// ---------------------------------------------------------------------------
// Key → id resolution
// ---------------------------------------------------------------------------

// verifyIDs maps dataset keys to live server ids, both ways.
type verifyIDs struct {
	entityID      map[string]string // entity key  → id
	entityKey     map[string]string // id          → entity key
	obligationID  map[string]string
	obligationKey map[string]string
	workflowID    map[string]string
	workflowKey   map[string]string

	// workflowCreated is each workflow's created_at UTC calendar date — the
	// column the raw export's workflow window filters on. It is a fact about
	// the run, not about the dataset, so it is read rather than recomputed.
	workflowCreated map[string]dateonly.Date

	entities    []verifyEntityRow
	obligations []verifyObligationTypeRow
	workflows   []verifyWorkflowRow

	// problems lists keys the live tenant does not carry.
	problems []string
}

// verifyResolveIDs matches the spec's tenant against the live tenant by
// natural key: entity name, obligation-type code, workflow name.
func verifyResolveIDs(ctx context.Context, c *verifyClient, t *oracleTenant) (*verifyIDs, error) {
	ids := &verifyIDs{
		entityID: map[string]string{}, entityKey: map[string]string{},
		obligationID: map[string]string{}, obligationKey: map[string]string{},
		workflowID: map[string]string{}, workflowKey: map[string]string{},
		workflowCreated: map[string]dateonly.Date{},
	}
	var err error
	if ids.entities, err = verifyListAll[verifyEntityRow](ctx, c, "/entities"); err != nil {
		return nil, err
	}
	if ids.obligations, err = verifyListAll[verifyObligationTypeRow](ctx, c, "/obligation-types"); err != nil {
		return nil, err
	}
	if ids.workflows, err = verifyListAll[verifyWorkflowRow](ctx, c, "/workflows"); err != nil {
		return nil, err
	}

	byEntityName := map[string]string{}
	for _, e := range ids.entities {
		byEntityName[e.Name] = e.ID
	}
	for _, e := range t.spec.Entities {
		id, ok := byEntityName[e.Name]
		if !ok {
			ids.problems = append(ids.problems, fmt.Sprintf("entity %s (%q) is not in the live tenant", e.Key, e.Name))
			continue
		}
		ids.entityID[e.Key] = id
		ids.entityKey[id] = e.Key
	}

	byObligationCode := map[string]string{}
	for _, o := range ids.obligations {
		byObligationCode[o.Code] = o.ID
	}
	for _, o := range t.spec.ObligationTypes {
		id, ok := byObligationCode[o.Code]
		if !ok {
			ids.problems = append(ids.problems, fmt.Sprintf("obligation type %s (code %q) is not in the live tenant", o.Key, o.Code))
			continue
		}
		ids.obligationID[o.Key] = id
		ids.obligationKey[id] = o.Key
	}

	byWorkflowName := map[string]verifyWorkflowRow{}
	for _, w := range ids.workflows {
		byWorkflowName[w.Name] = w
	}
	for _, w := range t.workflows {
		row, ok := byWorkflowName[w.spec.Name]
		if !ok {
			ids.problems = append(ids.problems, fmt.Sprintf("workflow %s (%q) is not in the live tenant", w.spec.Key, w.spec.Name))
			continue
		}
		ids.workflowID[w.spec.Key] = row.ID
		ids.workflowKey[row.ID] = w.spec.Key
		if created, err := time.Parse(time.RFC3339, row.CreatedAt); err == nil {
			ids.workflowCreated[w.spec.Key] = dateonly.FromTime(created.UTC())
		}
	}
	return ids, nil
}

// entityKeyOf / obligationKeyOf translate a live id back to a dataset key so
// differences read in dataset terms. An id the dataset does not know is
// returned as "<unknown id …>" rather than dropped.
func (ids *verifyIDs) entityKeyOf(id string) string {
	if id == "" || id == oracleUnknownKey {
		return oracleUnknownKey
	}
	if key, ok := ids.entityKey[id]; ok {
		return key
	}
	return "<unknown entity " + id + ">"
}

func (ids *verifyIDs) obligationKeyOf(id string) string {
	if id == "" || id == oracleUnknownKey {
		return oracleUnknownKey
	}
	if key, ok := ids.obligationKey[id]; ok {
		return key
	}
	return "<unknown obligation " + id + ">"
}

func (ids *verifyIDs) workflowKeyOf(id string) string {
	if key, ok := ids.workflowKey[id]; ok {
		return key
	}
	return "<unknown workflow " + id + ">"
}

// seedDates are the distinct UTC dates the tenant's workflows were created on
// — what the "SEED_DATE" placeholder in the dataset's export expectations
// stands for.
func (ids *verifyIDs) seedDates() []dateonly.Date {
	seen := map[string]dateonly.Date{}
	for _, d := range ids.workflowCreated {
		seen[d.String()] = d
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]dateonly.Date, 0, len(keys))
	for _, k := range keys {
		out = append(out, seen[k])
	}
	return out
}
