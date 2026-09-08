package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/seed/demo/scale"
	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/apiclient"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// `seed-demo bench`: the ADR-0021 rule 7 measurement.
//
//	seed-demo bench [--api URL] [--tenant scale] [--n 30] [--warmup 3] [--page-size 50]
//	                [--deep-page 200] [--json] [--out F] [--no-fail] [--email E --password P]
//
// It signs in as the tenant's admin, resolves the handful of ids the targets
// need (the current fiscal year, one VAT workflow), then hits each target
// sequentially: --warmup requests discarded, --n requests timed on the
// client's wall clock (request sent → body fully read, no JSON decoding), and
// reports p50 / p95 / max against the target's p95 budget. The exit status is
// non-zero on any miss unless --no-fail — the first run on a fresh fixture is
// the BASELINE and is expected to miss; record it, do not "fix" it here.
//
// Budgets (ADR-0021 rule 7): 500 ms for list reads — the task feed's first
// page and its deep page under both sorts, task-summary, workflow-stats, the
// entity search, one workflow's instances — and 2 000 ms for the aggregating
// reports (heatmap, compliance-status, tax-financial).
//
// Two targets are conditional. GET /reports/task-summary does not exist until
// increment 1: a 404 is reported as SKIP, not as a failure. And the entity
// search parameter is not honoured before increment 5 — the query validator
// silently drops query parameters it does not model — so the entity target
// measures the unfiltered first page until then and says so in its note
// (detected by comparing the search total with the unfiltered total).

// Budgets, in milliseconds.
const (
	benchListBudgetMs      = 500
	benchAggregateBudgetMs = 2000
)

// BenchDeps is everything `bench` needs; main.go fills the defaults and the
// raw arguments, tests fill the fields directly.
type BenchDeps struct {
	API      string
	Tenant   string
	Email    string
	Password string
	N        int
	Warmup   int
	PageSize int
	DeepPage int
	JSON     bool
	Out      string
	NoFail   bool

	Stdout io.Writer
	Stderr io.Writer
	// Client is an optional pre-built API client (tests inject one).
	Client *apiclient.Client
	// SpecPath is where the demo dataset's credentials come from when the
	// tenant is a demo one ("" = DefaultSpecPath).
	SpecPath string
	Now      func() time.Time
	Args     []string
}

func benchFlagSet(d *BenchDeps) *flag.FlagSet {
	fs := flag.NewFlagSet("seed-demo bench", flag.ContinueOnError)
	fs.StringVar(&d.API, "api", d.API, "API base URL (no /api prefix)")
	fs.StringVar(&d.Tenant, "tenant", d.Tenant, "tenant slug to measure (scale, or a demo tenant)")
	fs.StringVar(&d.Email, "email", d.Email, "sign-in e-mail (default: the tenant's admin — admin@scale.test, or the dataset's)")
	fs.StringVar(&d.Password, "password", d.Password, "sign-in password (default: the tenant admin's demo password)")
	fs.IntVar(&d.N, "n", d.N, "timed requests per target")
	fs.IntVar(&d.Warmup, "warmup", d.Warmup, "untimed requests per target before the timed ones")
	fs.IntVar(&d.PageSize, "page-size", d.PageSize, "page size of the paged targets")
	fs.IntVar(&d.DeepPage, "deep-page", d.DeepPage, "the deep page number of the task feed (offset = (page-1) × page size)")
	fs.BoolVar(&d.JSON, "json", d.JSON, "print the machine-readable result instead of the table")
	fs.StringVar(&d.Out, "out", d.Out, "also write the machine-readable result to this file")
	fs.BoolVar(&d.NoFail, "no-fail", d.NoFail, "exit 0 even when a target misses its budget")
	fs.Usage = func() {
		fmt.Fprintln(d.Stderr, "usage: seed-demo bench [flags]")
		fs.PrintDefaults()
	}
	return fs
}

// benchTarget is one measured endpoint.
type benchTarget struct {
	Name     string
	Path     string
	BudgetMs int
	// Note is context for the reader (a caveat about what was measured).
	Note string
	// SkipOn404 reports the target as SKIP when the route does not exist.
	SkipOn404 bool
}

// benchResult is one target's measurement.
type benchResult struct {
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	N        int     `json:"n"`
	P50Ms    float64 `json:"p50Ms"`
	P95Ms    float64 `json:"p95Ms"`
	MaxMs    float64 `json:"maxMs"`
	BudgetMs int     `json:"budgetMs"`
	// Result is PASS | FAIL | SKIP | ERROR.
	Result string `json:"result"`
	Note   string `json:"note,omitempty"`
}

// benchReport is the machine-readable output.
type benchReport struct {
	GeneratedAt string        `json:"generatedAt"`
	API         string        `json:"api"`
	Tenant      string        `json:"tenant"`
	N           int           `json:"n"`
	Warmup      int           `json:"warmup"`
	PageSize    int           `json:"pageSize"`
	DeepPage    int           `json:"deepPage"`
	Results     []benchResult `json:"results"`
	Failed      int           `json:"failed"`
}

// runBench is the subcommand.
func runBench(ctx context.Context, d BenchDeps) error {
	d.applyDefaults()
	if len(d.Args) > 0 {
		fs := benchFlagSet(&d)
		fs.SetOutput(d.Stderr)
		if err := fs.Parse(d.Args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() > 0 {
			return fmt.Errorf("unexpected argument %q", fs.Arg(0))
		}
	}
	if d.N < 1 || d.Warmup < 0 || d.PageSize < 1 || d.DeepPage < 1 {
		return errors.New("--n and --page-size and --deep-page must be at least 1, --warmup at least 0")
	}
	email, password, err := benchCredentials(d)
	if err != nil {
		return err
	}

	client := d.Client
	if client == nil {
		client = apiclient.New(d.API)
	}
	if err := client.WaitReady(ctx); err != nil {
		return fmt.Errorf("the API at %s is not ready: %w", client.BaseURL(), err)
	}
	c, err := verifyLogin(ctx, client, email, password)
	if err != nil {
		return err
	}
	if c.identity.Tenant.Slug != d.Tenant {
		return fmt.Errorf("%s belongs to tenant %q, not %q", email, c.identity.Tenant.Slug, d.Tenant)
	}

	targets, err := benchTargets(ctx, c, d)
	if err != nil {
		return err
	}

	report := benchReport{
		GeneratedAt: formatInstant(d.Now()), API: client.BaseURL(), Tenant: d.Tenant,
		N: d.N, Warmup: d.Warmup, PageSize: d.PageSize, DeepPage: d.DeepPage,
	}
	for _, target := range targets {
		result := benchMeasure(ctx, c.api, target, d.N, d.Warmup)
		if result.Result == "FAIL" || result.Result == "ERROR" {
			report.Failed++
		}
		report.Results = append(report.Results, result)
	}

	if d.Out != "" {
		if err := benchWriteJSON(d.Out, report); err != nil {
			return err
		}
	}
	if d.JSON {
		enc := json.NewEncoder(d.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		benchPrintTable(d.Stdout, report)
	}
	if report.Failed > 0 && !d.NoFail {
		return fmt.Errorf("%d of %d targets missed their budget", report.Failed, len(report.Results))
	}
	return nil
}

func (d *BenchDeps) applyDefaults() {
	if d.API == "" {
		d.API = DefaultAPIBaseURL
	}
	if d.Tenant == "" {
		d.Tenant = scale.DefaultSlug
	}
	if d.N == 0 {
		d.N = 30
	}
	// Defaults are applied before the flags parse, so `--warmup 0` still
	// wins: the flag's default is this value.
	if d.Warmup == 0 {
		d.Warmup = 3
	}
	if d.PageSize == 0 {
		d.PageSize = 50
	}
	if d.DeepPage == 0 {
		d.DeepPage = 200
	}
	if d.SpecPath == "" {
		d.SpecPath = DefaultSpecPath
	}
	if d.Stdout == nil {
		d.Stdout = io.Discard
	}
	if d.Stderr == nil {
		d.Stderr = io.Discard
	}
	if d.Now == nil {
		d.Now = time.Now
	}
}

// benchCredentials resolves who signs in: --email/--password, else the scale
// fixture's admin, else the dataset tenant's admin.
func benchCredentials(d BenchDeps) (string, string, error) {
	if d.Email != "" || d.Password != "" {
		if d.Email == "" || d.Password == "" {
			return "", "", errors.New("--email and --password go together")
		}
		return d.Email, d.Password, nil
	}
	if d.Tenant == scale.DefaultSlug {
		return scale.AdminEmail, scale.AdminPassword, nil
	}
	dataset, err := spec.Load(d.SpecPath)
	if err != nil {
		return "", "", fmt.Errorf("no --email/--password and the dataset could not be read for tenant %q: %w", d.Tenant, err)
	}
	for _, t := range dataset.Tenants {
		if t.Slug == d.Tenant || t.Key == d.Tenant {
			return t.Admin.Email, t.Admin.Password, nil
		}
	}
	return "", "", fmt.Errorf("tenant %q is neither the scale fixture nor in %s; pass --email and --password",
		d.Tenant, d.SpecPath)
}

// benchTargets resolves the target list against the live tenant.
func benchTargets(ctx context.Context, c *verifyClient, d BenchDeps) ([]benchTarget, error) {
	zone, err := time.LoadLocation(c.identity.Tenant.Timezone)
	if err != nil {
		zone = time.UTC
	}
	today := dateonly.FromTime(d.Now().In(zone))
	fiscalYear := strconv.Itoa(today.Year)

	vatWorkflow, err := benchFindVATWorkflow(ctx, c)
	if err != nil {
		return nil, err
	}

	deepOffset := (d.DeepPage - 1) * d.PageSize
	feed := func(sort string, offset int) string {
		return fmt.Sprintf("/reports/task-instances?limit=%d&offset=%d&sort=%s", d.PageSize, offset, sort)
	}
	searchPath := "/entities?search=" + url.QueryEscape("Mül") + "&limit=20"
	searchNote, err := benchSearchNote(ctx, c, searchPath)
	if err != nil {
		return nil, err
	}

	return []benchTarget{
		{Name: fmt.Sprintf("task feed p1 (dueDate:asc, %d/page)", d.PageSize), Path: feed("dueDate:asc", 0), BudgetMs: benchListBudgetMs},
		{Name: fmt.Sprintf("task feed p%d (dueDate:asc, offset %d)", d.DeepPage, deepOffset), Path: feed("dueDate:asc", deepOffset), BudgetMs: benchListBudgetMs},
		{Name: fmt.Sprintf("task feed p1 (createdAt:asc, %d/page)", d.PageSize), Path: feed("createdAt:asc", 0), BudgetMs: benchListBudgetMs},
		{Name: fmt.Sprintf("task feed p%d (createdAt:asc, offset %d)", d.DeepPage, deepOffset), Path: feed("createdAt:asc", deepOffset), BudgetMs: benchListBudgetMs},
		{Name: "task summary", Path: "/reports/task-summary", BudgetMs: benchListBudgetMs, SkipOn404: true},
		{Name: "workflow stats", Path: "/reports/workflow-stats", BudgetMs: benchListBudgetMs},
		{Name: "compliance heatmap (FY " + fiscalYear + ", period view)",
			Path: "/reports/compliance-heatmap?year=" + fiscalYear + "&viewMode=period", BudgetMs: benchAggregateBudgetMs},
		{Name: fmt.Sprintf("compliance status p1 (%d/page)", d.PageSize),
			Path: fmt.Sprintf("/reports/compliance-status?limit=%d&offset=0", d.PageSize), BudgetMs: benchAggregateBudgetMs},
		{Name: "entity search (Mül, 20)", Path: searchPath, BudgetMs: benchListBudgetMs, Note: searchNote},
		{Name: fmt.Sprintf("workflow instances (VAT workflow, %d/page)", d.PageSize),
			Path: fmt.Sprintf("/task-instances?workflowId=%s&limit=%d", url.QueryEscape(vatWorkflow), d.PageSize), BudgetMs: benchListBudgetMs},
		{Name: "tax financial (by entity)", Path: "/reports/tax-financial?groupBy=entity", BudgetMs: benchAggregateBudgetMs},
	}, nil
}

// benchFindVATWorkflow picks one started VAT workflow of the tenant (the
// obligation type whose template is VAT; the first active workflow on it).
func benchFindVATWorkflow(ctx context.Context, c *verifyClient) (string, error) {
	types, err := verifyListAll[verifyObligationTypeRow](ctx, c, "/obligation-types")
	if err != nil {
		return "", err
	}
	vatTypes := map[string]bool{}
	for _, t := range types {
		if t.Template == "VAT" {
			vatTypes[t.ID] = true
		}
	}
	if len(vatTypes) == 0 {
		return "", errors.New("the tenant has no VAT obligation type")
	}
	workflows, err := verifyListAll[verifyWorkflowRow](ctx, c, "/workflows")
	if err != nil {
		return "", err
	}
	var fallback string
	for _, w := range workflows {
		if w.ObligationTypeID == nil || !vatTypes[*w.ObligationTypeID] {
			continue
		}
		if w.Status == "active" {
			return w.ID, nil
		}
		if fallback == "" && w.Status != "draft" {
			fallback = w.ID
		}
	}
	if fallback == "" {
		return "", errors.New("the tenant has no started VAT workflow")
	}
	return fallback, nil
}

// benchSearchNote reports whether the entity search parameter is honoured:
// when the search total equals the unfiltered total, the API build ignores
// it (pre-increment 5) and the target measures the unfiltered first page.
func benchSearchNote(ctx context.Context, c *verifyClient, searchPath string) (string, error) {
	var page []json.RawMessage
	searched, err := c.api.GetPaginated(ctx, searchPath, &page)
	if err != nil {
		if apiclient.IsStatus(err, 400) {
			return "the API rejects the search parameter (400); the target measures /entities?limit=20 instead", nil
		}
		return "", fmt.Errorf("GET %s: %w", searchPath, err)
	}
	unfiltered, err := c.api.GetPaginated(ctx, "/entities?limit=20", &page)
	if err != nil {
		return "", fmt.Errorf("GET /entities?limit=20: %w", err)
	}
	if searched.Present && unfiltered.Present && searched.Total == unfiltered.Total && unfiltered.Total > 1 {
		return fmt.Sprintf("search is not honoured by this API build (total %d = unfiltered total); "+
			"measures the unfiltered first page", searched.Total), nil
	}
	return "", nil
}

// benchMeasure runs one target: warm-ups discarded, n timed requests.
func benchMeasure(ctx context.Context, api *apiclient.Client, t benchTarget, n, warmup int) benchResult {
	result := benchResult{Name: t.Name, Path: t.Path, BudgetMs: t.BudgetMs, Note: t.Note}
	path := t.Path
	if strings.Contains(t.Note, "rejects the search parameter") {
		path = "/entities?limit=20"
		result.Path = path
	}
	samples := make([]float64, 0, n)
	for i := 0; i < warmup+n; i++ {
		started := time.Now()
		_, _, err := api.GetRaw(ctx, path)
		elapsed := time.Since(started)
		if err != nil {
			if t.SkipOn404 && apiclient.IsNotFound(err) {
				result.Result = "SKIP"
				result.Note = benchJoinNotes(result.Note, "endpoint not available on this API build (404)")
				return result
			}
			result.Result = "ERROR"
			result.Note = benchJoinNotes(result.Note, err.Error())
			return result
		}
		if i >= warmup {
			samples = append(samples, float64(elapsed.Microseconds())/1000)
		}
	}
	result.N = len(samples)
	result.P50Ms = benchPercentile(samples, 0.50)
	result.P95Ms = benchPercentile(samples, 0.95)
	result.MaxMs = benchPercentile(samples, 1)
	result.Result = "PASS"
	if result.P95Ms > float64(t.BudgetMs) {
		result.Result = "FAIL"
	}
	return result
}

func benchJoinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "; " + b
	}
}

// benchPercentile is the nearest-rank percentile of the samples (p in (0, 1]).
func benchPercentile(samples []float64, p float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// benchPrintTable renders the human table plus the notes.
func benchPrintTable(out io.Writer, r benchReport) {
	fmt.Fprintf(out, "bench: %s, tenant %s, %d warm-up + %d timed requests per target, p95 budgets per ADR-0021 rule 7\n\n",
		r.API, r.Tenant, r.Warmup, r.N)
	fmt.Fprintf(out, "  %-52s %4s %9s %9s %9s %7s   %s\n", "TARGET", "N", "P50 MS", "P95 MS", "MAX MS", "BUDGET", "RESULT")
	for _, res := range r.Results {
		if res.N == 0 {
			fmt.Fprintf(out, "  %-52s %4s %9s %9s %9s %7d   %s\n", res.Name, "-", "-", "-", "-", res.BudgetMs, res.Result)
			continue
		}
		fmt.Fprintf(out, "  %-52s %4d %9.1f %9.1f %9.1f %7d   %s\n",
			res.Name, res.N, res.P50Ms, res.P95Ms, res.MaxMs, res.BudgetMs, res.Result)
	}
	notes := false
	for _, res := range r.Results {
		if res.Note == "" {
			continue
		}
		if !notes {
			fmt.Fprintln(out, "\nnotes:")
			notes = true
		}
		fmt.Fprintf(out, "  - %s: %s\n", res.Name, res.Note)
	}
	fmt.Fprintf(out, "\n%d of %d targets within budget", len(r.Results)-r.Failed, len(r.Results))
	if r.Failed > 0 {
		fmt.Fprintf(out, ", %d missed", r.Failed)
	}
	fmt.Fprintln(out)
}

func benchWriteJSON(path string, r benchReport) error {
	encoded, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
