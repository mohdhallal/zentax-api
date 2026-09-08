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
//
//	seed-demo verify --scale [--scale-config ./seed-demo-scale.json] --api …
//
// --scale verifies the scale fixture (seed/demo/scale) instead of the dataset:
// the tenant is regenerated from the generation record `seed-demo scale`
// wrote — never from the defaults, because the status distribution was drawn
// for the record's asOf and a default-asOf regeneration on a later day
// describes a different world — and the same check families run against the
// scale tenant alone. What cannot run there (the dataset's static expectedAsOf
// tables — a generated fixture declares none) is reported as SKIPPED, never
// dropped silently. The run is long (~10⁵ instances, ~2,000 previews) and
// prints its progress on stderr.

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

	"github.com/mohamadhallal/zentax-api/seed/demo/scale"
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
	// Scale verifies the scale fixture instead of the dataset (see the file
	// comment). It does not combine with Only: the fixture is one tenant.
	Scale bool
	// ScaleConfig is the generation record `seed-demo scale` wrote; the
	// fixture is recomputed from exactly its config.
	ScaleConfig string

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
	fs.BoolVar(&d.Scale, "scale", d.Scale,
		"verify the scale fixture (seed/demo/scale) instead of the dataset: recompute it from the\n"+
			"\tgeneration record (--scale-config) and diff the scale tenant alone. Does not combine with --only;\n"+
			"\t--spec and --in are not read")
	// No backquotes in this usage string: the flag package would read them as
	// the value's placeholder name.
	fs.StringVar(&d.ScaleConfig, "scale-config", d.ScaleConfig,
		"--scale: the generation record that seed-demo scale --out wrote (its config, asOf above all, is\n"+
			"\twhat the fixture is recomputed from)")
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
	if d.Scale && strings.TrimSpace(d.Only) != "" {
		return errors.New("--scale and --only do not combine: verify either the scale fixture or dataset tenants")
	}

	// The world under test: the dataset, or the scale fixture regenerated from
	// its record. In scale mode there is no seeder key → id file to cross-check
	// (the record is cross-checked instead) and no static tables to compare.
	var (
		dataset  *spec.Spec
		scaleRun *verifyScaleRun
		idMap    *seedOutput
		err      error
	)
	if d.Scale {
		scaleRun, err = verifyLoadScale(d.ScaleConfig)
		if err != nil {
			return err
		}
		dataset = scaleRun.spec
	} else {
		if dataset, err = spec.LoadAndValidate(d.Spec); err != nil {
			return err
		}
		if idMap, err = verifyReadIDMap(d.In); err != nil {
			return err
		}
	}
	world, err := buildOracleWorld(dataset, d.Now(), todayOverride)
	if err != nil {
		return fmt.Errorf("recompute the oracle: %w", err)
	}
	tenants, err := verifySelectTenants(world, d.Only)
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
		// A long run (the scale fixture, or any tenant of its size) says where
		// it is; a demo tenant is done before a line would help.
		var progress *verifyProgress
		if d.Scale || len(tenant.instances) >= verifyProgressThreshold {
			progress = &verifyProgress{w: d.Stderr, tenant: tenant.spec.Key}
		}
		if err := verifyTenant(ctx, report, client, dataset, tenant, idMap, scaleRun, progress); err != nil {
			return fmt.Errorf("tenant %s: %w", tenant.spec.Key, err)
		}
		if scaleRun != nil {
			// A generated fixture declares no expectedAsOf tables: the oracle's
			// recomputation is its only reference. Say so rather than run
			// nothing.
			report.check(tenant.spec.Key, "static-expectations", "").
				skip("the scale fixture is generated, not declared: it has no static expectedAsOf tables to cross-check")
			continue
		}
		verifyStaticTables(report, dataset, tenant.spec.Key, d.StrictStatic)
	}

	if d.JSON {
		if err := report.writeJSON(d.Stdout); err != nil {
			return err
		}
	} else {
		if scaleRun != nil {
			fmt.Fprintf(d.Stdout, "verify --scale: %s, generation record %s\n", client.BaseURL(), scaleRun.path)
			fmt.Fprintf(d.Stdout, "  %s\n", scaleRun.describe())
		} else {
			fmt.Fprintf(d.Stdout, "verify: %s, spec %s\n", client.BaseURL(), d.Spec)
		}
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
	if d.ScaleConfig == "" {
		d.ScaleConfig = DefaultScaleOutputPath
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
// The scale fixture
// ---------------------------------------------------------------------------

// verifyScaleRun is a --scale run's world: the generation record, the config
// restored from it, and the tenant regenerated from that config.
type verifyScaleRun struct {
	path   string
	record scaleOutput
	config scale.Config
	spec   *spec.Spec
}

// verifyLoadScale reads the generation record and regenerates the fixture
// from its config — the record's, not the defaults: the statuses were drawn
// for the record's asOf, so any other asOf describes a different tenant.
func verifyLoadScale(path string) (*verifyScaleRun, error) {
	record, err := readScaleOutput(path)
	if err != nil {
		return nil, fmt.Errorf("--scale needs the generation record `seed-demo scale` wrote (--scale-config): %w", err)
	}
	cfg, err := scaleConfigFromOutput(record)
	if err != nil {
		return nil, err
	}
	generated, err := scale.Spec(cfg)
	if err != nil {
		return nil, fmt.Errorf("regenerate the scale fixture from %s: %w", path, err)
	}
	return &verifyScaleRun{path: path, record: record, config: cfg, spec: generated}, nil
}

// describe renders the config the world was recomputed from.
func (s *verifyScaleRun) describe() string {
	c := s.config
	return fmt.Sprintf("tenant %s (%q, %s), seed %d, %d entities, %d years, asOf %s — generated %s",
		c.Slug, c.Name, c.Timezone, c.Seed, c.Entities, c.Years, c.AsOf, s.record.GeneratedAt)
}

// checkScaleRecord cross-checks the generation record against the live
// tenant, the way checkKeyResolution cross-checks the seeder's key → id file
// for the dataset: the ids `scale` reported and the counts it wrote must be
// the tenant verify just read. A record for a different tenant (a re-run
// with another slug, a stale file) is caught here rather than half-way
// through the report checks.
func (r *verifyRun) checkScaleRecord() {
	if r.scale == nil {
		return
	}
	c := r.check("scale-record", r.scale.path)
	record := r.scale.record
	c.equal("config.slug", "", r.tenant.spec.Slug, record.Config.Slug)
	c.equal("config.timezone", "", r.tenant.spec.Timezone, record.Config.Timezone)
	if record.TenantID != "" {
		c.equal("tenantId", "", record.TenantID, r.client.identity.Tenant.ID)
	}
	if record.AdminID != "" {
		c.equal("adminId", "", record.AdminID, r.client.identity.ID)
	}
	c.equal("counts.entities", "", record.Counts.Entities, len(r.ids.entities))
	c.equal("counts.obligationTypes", "", record.Counts.ObligationTypes, len(r.ids.obligations))
	c.equal("counts.workflows", "", record.Counts.Workflows, len(r.ids.workflows))
	c.equal("counts.instances", "", record.Counts.Instances, len(r.instances))
	// The regenerated spec must be the size the record says was written —
	// otherwise the record and the generator have drifted apart.
	c.equal("generated.workflows", "", record.Counts.Workflows, len(r.tenant.workflows))
	c.equal("generated.instances", "", record.Counts.Instances, len(r.tenant.instances))
}

// ---------------------------------------------------------------------------
// Progress
// ---------------------------------------------------------------------------

// verifyProgressThreshold is the tenant size (expected instances) from which
// verify narrates its long loops — the instance walk, the per-workflow
// previews, the per-entity period lists and the paged reports — so an
// operator can tell a slow run from a stuck one.
const verifyProgressThreshold = 5000

// verifyProgressEvery is how many previews / period lists pass between two
// progress lines.
const verifyProgressEvery = 250

// verifyProgress writes progress lines for one tenant. A nil *verifyProgress
// is silent, so callers never test for it.
type verifyProgress struct {
	w      io.Writer
	tenant string
}

func (p *verifyProgress) step(format string, args ...any) {
	if p == nil || p.w == nil {
		return
	}
	fmt.Fprintf(p.w, "  %s: %s\n", p.tenant, fmt.Sprintf(format, args...))
}

// every reports the i-th of n steps at the interval, and always the last.
func (p *verifyProgress) every(what string, i, n int) {
	if p == nil || n == 0 {
		return
	}
	if i%verifyProgressEvery == 0 || i == n {
		p.step("%s %d/%d", what, i, n)
	}
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

	// scale is set on a --scale run (the record to cross-check); progress is
	// where a long run narrates itself (nil = silent).
	scale    *verifyScaleRun
	progress *verifyProgress
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
	scaleRun *verifyScaleRun, progress *verifyProgress,
) error {
	admin := tenant.spec.Admin
	client, err := verifyLogin(ctx, base, admin.Email, admin.Password)
	if err != nil {
		return err
	}
	progress.step("signed in as %s; resolving ids", admin.Email)
	ids, err := verifyResolveIDs(ctx, client, tenant)
	if err != nil {
		return err
	}
	instances, err := verifyInstances(ctx, client, progress)
	if err != nil {
		return err
	}

	run := &verifyRun{
		ctx: ctx, report: report, spec: loaded, tenant: tenant,
		client: client, ids: ids, instances: instances,
		byLiveID: map[string]*oracleInstance{}, liveByRef: map[string]verifyInstanceRow{},
		scale: scaleRun, progress: progress,
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
	run.checkScaleRecord()
	run.checkPreviews()
	run.checkPeriods()
	run.checkHeatmaps()
	run.checkComplianceStatus()
	run.checkTaxFinancial()
	run.checkExportRaw()
	run.checkWorkflowStats()
	run.checkDashboard()
	run.checkTaskSummary()
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
