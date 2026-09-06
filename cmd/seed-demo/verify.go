package main

// `seed-demo verify`: recompute the demo dataset's expectations from the spec
// and diff them against the live API.
//
//	seed-demo verify --spec seed/demo/dataset.json --api http://localhost:3000
//
// For each tenant it signs in as the admin, resolves the dataset's keys to the
// live server ids, and then calls every report the dataset has expectations
// for — with every filter combination those expectations cover, plus the
// unfiltered set — comparing each answer against the oracle (oracle*.go),
// never against the numbers written in the dataset. Those static numbers are
// still checked, as the dataset's own internal consistency check, and reported
// as INFO unless --strict-static promotes them.
//
// Exit is non-zero on any difference.
//
// Verify needs nothing but the spec and the API: keys resolve from the live
// tenant by natural key, so a database seeded by an earlier run verifies
// without the seeder's key → id file. When that file IS on disk (--in,
// defaulting to the path seed writes) it is cross-checked as well.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/apiclient"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// VerifyDeps is everything `verify` needs. main.go hands it the subcommand's
// raw arguments (Args) plus the defaults; the fields below act as those
// defaults and are overwritten by the flags verify parses itself. Tests fill
// the fields directly and leave Args empty.
type VerifyDeps struct {
	// Spec is the path to dataset.json.
	Spec string
	// API is the base URL of the Go API (no /api prefix — that is the UI
	// proxy's).
	API string
	// In is the seeder's key → id map. Optional in every sense: verify
	// resolves keys from the live tenant, and a missing file is not an error;
	// when the file is there it is cross-checked.
	In string
	// Only limits the run to the named tenants (dataset keys or slugs,
	// comma-separated).
	Only string
	// Today overrides every tenant's civil today (YYYY-MM-DD) for a what-if
	// run. The live API still answers on its own clock, so a Today far from
	// the real one is expected to differ — the run says so up front.
	Today string
	// JSON prints the machine-readable result instead of the text report.
	JSON bool
	// StrictStatic makes drift between the dataset's static expectedAsOf
	// tables and the recomputed oracle a failure instead of INFO.
	StrictStatic bool

	Stdout io.Writer
	Stderr io.Writer
	// Client is an optional pre-built API client (tests inject one).
	Client *apiclient.Client
	// Now is the clock each tenant's civil today is read from.
	Now func() time.Time
	// Args are the raw arguments after the subcommand name. When present they
	// are parsed as verify's flags, using the fields above as defaults.
	Args []string
}

// verifyFlagSet registers verify's flags onto deps.
func verifyFlagSet(d *VerifyDeps) *flag.FlagSet {
	fs := flag.NewFlagSet("seed-demo verify", flag.ContinueOnError)
	fs.StringVar(&d.Spec, "spec", d.Spec, "path to the demo dataset")
	fs.StringVar(&d.API, "api", d.API, "API base URL (no /api prefix — that is the UI proxy's)")
	fs.StringVar(&d.In, "in", d.In, "the seeder's key → id map, cross-checked when it exists")
	fs.StringVar(&d.Only, "only", d.Only, "comma-separated tenant keys or slugs to verify (default: every tenant)")
	fs.StringVar(&d.Today, "today", d.Today,
		"recompute against this civil date (YYYY-MM-DD) instead of each tenant's today — a what-if run")
	fs.BoolVar(&d.JSON, "json", d.JSON, "print the machine-readable result")
	fs.BoolVar(&d.StrictStatic, "strict-static", d.StrictStatic,
		"fail on drift between the dataset's static expectedAsOf tables and the recomputed oracle")
	return fs
}

// runVerify is the subcommand entry point. It returns an error when anything
// differs, so the process exits non-zero.
func runVerify(ctx context.Context, d VerifyDeps) error {
	d.applyDefaults()
	if len(d.Args) > 0 {
		fs := verifyFlagSet(&d)
		fs.SetOutput(d.Stderr)
		if err := fs.Parse(d.Args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil // the flag set has printed the usage
			}
			return err
		}
		if fs.NArg() > 0 {
			return fmt.Errorf("unexpected argument %q", fs.Arg(0))
		}
	}

	var todayOverride dateonly.Date
	if d.Today != "" {
		parsed, err := dateonly.Parse(d.Today)
		if err != nil {
			return fmt.Errorf("--today must be a YYYY-MM-DD date, not %q", d.Today)
		}
		todayOverride = parsed
	}

	dataset, err := spec.LoadAndValidate(d.Spec)
	if err != nil {
		return err
	}
	world, err := buildOracleWorld(dataset, d.Now(), todayOverride)
	if err != nil {
		return fmt.Errorf("recompute the oracle: %w", err)
	}
	tenants, err := verifySelectTenants(world, d.Only)
	if err != nil {
		return err
	}
	idMap, err := verifyReadIDMap(d.In)
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

	report := &verifyReport{StrictStatic: d.StrictStatic}
	for _, tenant := range tenants {
		if err := verifyTenant(ctx, report, client, dataset, tenant, idMap); err != nil {
			return fmt.Errorf("tenant %s: %w", tenant.spec.Key, err)
		}
		verifyStaticTables(report, dataset, tenant.spec.Key, d.StrictStatic)
	}

	if d.JSON {
		if err := report.writeJSON(d.Stdout); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(d.Stdout, "verify: %s, spec %s\n", client.BaseURL(), d.Spec)
		if !todayOverride.IsZero() {
			fmt.Fprintf(d.Stdout,
				"WHAT-IF RUN: the oracle uses today=%s while the API answers on its own clock;\n"+
					"             classification differences are expected unless the two agree.\n", todayOverride)
		}
		fmt.Fprintln(d.Stdout)
		report.writeText(d.Stdout)
	}

	summary := report.summary()
	if summary.Failed > 0 {
		return fmt.Errorf("%d of %d checks failed (%d differences)",
			summary.Failed, summary.Checks, summary.Diffs)
	}
	return nil
}

// verifySelectTenants applies --only, matching a tenant by KEY or by SLUG
// (they differ: the acme tenant's slug is acme-demo). Order is always the
// dataset's.
func verifySelectTenants(world *oracleWorld, only string) ([]*oracleTenant, error) {
	if strings.TrimSpace(only) == "" {
		return world.tenants, nil
	}
	wanted := map[string]bool{}
	for _, name := range strings.Split(only, ",") {
		if name = strings.TrimSpace(name); name != "" {
			wanted[name] = true
		}
	}
	selected := []*oracleTenant{}
	matched := map[string]bool{}
	for _, tenant := range world.tenants {
		switch {
		case wanted[tenant.spec.Key]:
			matched[tenant.spec.Key] = true
		case wanted[tenant.spec.Slug]:
			matched[tenant.spec.Slug] = true
		default:
			continue
		}
		selected = append(selected, tenant)
	}
	unknown := []string{}
	for name := range wanted {
		if !matched[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("--only names %s, which is neither a tenant key nor a slug in the dataset",
			strings.Join(unknown, ", "))
	}
	if len(selected) == 0 {
		return nil, errors.New("--only was given but names no tenant")
	}
	return selected, nil
}

func (d *VerifyDeps) applyDefaults() {
	if d.Spec == "" {
		d.Spec = DefaultSpecPath
	}
	if d.API == "" {
		d.API = DefaultAPIBaseURL
	}
	if d.In == "" {
		d.In = DefaultOutputPath
	}
	if d.Stdout == nil {
		d.Stdout = os.Stdout
	}
	if d.Stderr == nil {
		d.Stderr = os.Stderr
	}
	if d.Now == nil {
		d.Now = time.Now
	}
}

// verifyReadIDMap reads the seeder's key → id file when it is there. A missing
// file is not an error: verify never needs it.
func verifyReadIDMap(path string) (*seedOutput, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the key → id map %s: %w", path, err)
	}
	var out seedOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse the key → id map %s: %w", path, err)
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// One tenant
// ---------------------------------------------------------------------------

// verifyRun is one tenant's verification: the oracle on one side, the live
// tenant on the other, and the report they disagree into.
type verifyRun struct {
	ctx    context.Context
	report *verifyReport
	spec   *spec.Spec
	tenant *oracleTenant
	client *verifyClient
	ids    *verifyIDs

	// instances is every live enriched instance. byLiveID maps a live instance
	// id to the oracle instance it must be (the compliance rows are matched
	// through it); liveByRef is the same rows by dataset reference.
	instances []verifyInstanceRow
	byLiveID  map[string]*oracleInstance
	liveByRef map[string]verifyInstanceRow

	// stats caches GET /reports/workflow-stats (two checks read it).
	stats map[string]verifyWorkflowStat
}

// expectations is the dataset's static table for this tenant, if it declares
// one for its own asOf date. The filter keys of those tables are what verify
// exercises against the live API; the numbers in them are only cross-checked.
func (r *verifyRun) expectations() (spec.Expectations, bool) {
	byTenant, ok := r.spec.ExpectedAsOf[r.spec.AsOf.String()]
	if !ok {
		return spec.Expectations{}, false
	}
	e, ok := byTenant[r.tenant.spec.Key]
	return e, ok
}

func verifyTenant(
	ctx context.Context, report *verifyReport, base *apiclient.Client,
	loaded *spec.Spec, tenant *oracleTenant, idMap *seedOutput,
) error {
	admin := tenant.spec.Admin
	client, err := verifyLogin(ctx, base, admin.Email, admin.Password)
	if err != nil {
		return err
	}
	ids, err := verifyResolveIDs(ctx, client, tenant)
	if err != nil {
		return err
	}
	instances, err := verifyInstances(ctx, client)
	if err != nil {
		return err
	}

	run := &verifyRun{
		ctx: ctx, report: report, spec: loaded, tenant: tenant,
		client: client, ids: ids, instances: instances,
		byLiveID: map[string]*oracleInstance{}, liveByRef: map[string]verifyInstanceRow{},
	}

	info := verifyTenantInfo{
		Key: tenant.spec.Key, Slug: tenant.spec.Slug, Timezone: tenant.spec.Timezone,
		Today: tenant.today.String(), TodayOverride: tenant.todayOverridden,
		Instances: len(tenant.instances), LiveInstances: len(instances),
	}
	dates := make([]string, 0, 2)
	for _, d := range ids.seedDates() {
		dates = append(dates, d.String())
	}
	info.SeedDatesFound = strings.Join(dates, ", ")
	report.Tenants = append(report.Tenants, info)

	run.checkTenantRegistry()
	run.checkInstances()
	run.checkKeyResolution(idMap)
	run.checkPreviews()
	run.checkPeriods()
	run.checkHeatmaps()
	run.checkComplianceStatus()
	run.checkTaxFinancial()
	run.checkExportRaw()
	run.checkWorkflowStats()
	run.checkDashboard()
	run.checkProjectParticipation()
	return nil
}

func (r *verifyRun) check(name, filter string) *verifyCheck {
	return r.report.check(r.tenant.spec.Key, name, filter)
}

// ---------------------------------------------------------------------------
// Filter keys
// ---------------------------------------------------------------------------

// verifyFilterKey is one key of the dataset's expectedAsOf maps: a query
// string with dataset keys where the API wants ids ("year=all&entityId=acme.de
// &viewMode=period"), the bare word "all", or — for the export — a leading
// dataset name ("tasks&category=project").
type verifyFilterKey struct {
	Raw    string
	Params map[string]string
	Bare   []string
}

func verifyParseFilterKey(raw string) verifyFilterKey {
	key := verifyFilterKey{Raw: raw, Params: map[string]string{}}
	for _, token := range strings.Split(raw, "&") {
		if token == "" {
			continue
		}
		name, value, found := strings.Cut(token, "=")
		if !found {
			key.Bare = append(key.Bare, name)
			continue
		}
		key.Params[name] = value
	}
	return key
}

// oracleFilters reads the filter trio out of the key (dataset keys, not ids).
func (k verifyFilterKey) oracleFilters() oracleFilters {
	return oracleFilters{
		Year:          k.Params["year"],
		EntityKey:     k.Params["entityId"],
		ObligationKey: k.Params["obligationTypeId"],
	}
}

// query translates the key into the live query string: entity / obligation
// keys become ids, everything else passes through unchanged (including the
// legacy "all", which the API normalizes to "no filter").
func (k verifyFilterKey) query(ids *verifyIDs, seedDates []dateonly.Date) (map[string]string, error) {
	out := map[string]string{}
	for name, value := range k.Params {
		switch name {
		case "entityId":
			if !oracleFilterSet(value) {
				out[name] = value
				continue
			}
			id, ok := ids.entityID[value]
			if !ok {
				return nil, fmt.Errorf("entity key %q does not resolve to a live id", value)
			}
			out[name] = id
		case "obligationTypeId":
			if !oracleFilterSet(value) {
				out[name] = value
				continue
			}
			id, ok := ids.obligationID[value]
			if !ok {
				return nil, fmt.Errorf("obligation-type key %q does not resolve to a live id", value)
			}
			out[name] = id
		case "dateFrom", "dateTo":
			resolved, err := verifyResolveDate(name, value, seedDates)
			if err != nil {
				return nil, err
			}
			out[name] = resolved
		default:
			out[name] = value
		}
	}
	return out, nil
}

// verifyResolveDate expands the dataset's SEED_DATE placeholder: the day the
// workflows were created. A run that straddled UTC midnight has two; dateFrom
// then takes the earliest and dateTo the latest, which is the window the
// placeholder means ("the seed run").
func verifyResolveDate(name, value string, seedDates []dateonly.Date) (string, error) {
	if value != "SEED_DATE" {
		return value, nil
	}
	if len(seedDates) == 0 {
		return "", errors.New("SEED_DATE: the live tenant has no workflow to read a creation date from")
	}
	if name == "dateFrom" {
		return seedDates[0].String(), nil
	}
	return seedDates[len(seedDates)-1].String(), nil
}

// exportFilters reads the export's filters out of the key, resolving
// SEED_DATE the same way the query does so both sides agree.
func (k verifyFilterKey) exportFilters(seedDates []dateonly.Date) (oracleExportFilters, error) {
	f := oracleExportFilters{
		EntityKey:     k.Params["entityId"],
		ObligationKey: k.Params["obligationTypeId"],
		Category:      k.Params["category"],
	}
	for _, name := range []string{"dateFrom", "dateTo"} {
		raw, ok := k.Params[name]
		if !ok || raw == "" {
			continue
		}
		resolved, err := verifyResolveDate(name, raw, seedDates)
		if err != nil {
			return f, err
		}
		parsed, err := dateonly.Parse(resolved)
		if err != nil {
			return f, fmt.Errorf("%s: %q is not a YYYY-MM-DD date", name, resolved)
		}
		if name == "dateFrom" {
			f.DateFrom = parsed
		} else {
			f.DateTo = parsed
		}
	}
	return f, nil
}

// dataset is the export dataset the key names (its leading bare token).
func (k verifyFilterKey) dataset() string {
	for _, bare := range k.Bare {
		switch bare {
		case oracleDatasetWorkflows, oracleDatasetTasks, oracleDatasetTaxData:
			return bare
		}
	}
	return oracleDatasetTasks // the endpoint's own default
}

// verifyExpectationKeys returns a map's keys in a stable order: the dataset's
// own maps are unordered, and a run must be reproducible.
func verifyExpectationKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
