package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/platform/database"
	platformseed "github.com/mohamadhallal/zentax-api/platform/seed"
	"github.com/mohamadhallal/zentax-api/seed/demo/scale"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// `seed-demo scale`: generate the scale fixture and bulk-write it.
//
//	seed-demo scale --yes [--database-url DSN] [--seed N] [--entities N] [--years N]
//	                [--as-of YYYY-MM-DD] [--slug scale] [--name …] [--timezone Europe/Berlin] [--dry-run]
//
// The fixture is one big tenant (default 48 entities × 5 obligation types × 8
// fiscal years → 1,920 started workflows, 97,152 task instances, plus five
// drafts) generated from a seed — seed/demo/scale documents the shape. It is
// what ADR-0021 rule 7 (p95 ≤ 500 ms list reads at 10⁵ instances) and the
// pagination proofs are measured against; `seed-demo bench` reads it.
//
// The same guards as `seed` apply: the connection must be the APPLICATION
// role (a BYPASSRLS connection would mis-seed the predefined data templates —
// see ensureRLSEnforced), the database must be local (resetGuard — this
// command writes a hundred thousand rows), the slug and every e-mail must be
// free, and --yes is required. Remove the tenant again with
// `seed-demo reset --scale --yes --admin-dsn …`.

// ScaleDeps is everything `scale` needs; main.go fills the defaults and the
// raw arguments, tests fill the fields directly.
type ScaleDeps struct {
	// DatabaseURL is the APPLICATION role DSN ("" = config.Load()).
	DatabaseURL string
	// Config is the generator configuration (zero fields take the defaults).
	Config scale.Config
	// Yes confirms the write.
	Yes bool
	// DryRun generates and reports, writing nothing.
	DryRun bool
	// OutputPath is where the generation record (the Config the fixture was
	// derived from, plus ids and counts) is written; `verify --scale` needs
	// the same Config to recompute the same world.
	OutputPath string

	Stdout io.Writer
	Stderr io.Writer
	// DB is an optional pre-opened connection (tests inject one); when nil
	// the DSN is resolved and opened.
	DB platformseed.DB
	// Now is the clock; the tenant's civil today defaults AsOf.
	Now func() time.Time
	// Args are the raw arguments after the subcommand name.
	Args []string

	// parseAsOf applies --as-of after the flag set has parsed.
	parseAsOf func() error
}

func scaleFlagSet(d *ScaleDeps) *flag.FlagSet {
	fs := flag.NewFlagSet("seed-demo scale", flag.ContinueOnError)
	fs.StringVar(&d.DatabaseURL, "database-url", d.DatabaseURL,
		"database DSN for the APPLICATION role (default: config.Load(), i.e. DATABASE_URL or\n"+
			"\tdeployment/config_files/$APP_ENV.json). It must NOT bypass RLS")
	fs.Int64Var(&d.Config.Seed, "seed", scale.DefaultSeed, "PRNG seed; the same seed yields the same tenant")
	fs.IntVar(&d.Config.Entities, "entities", scale.DefaultEntities, "entities in the 3-level tree")
	fs.IntVar(&d.Config.Years, "years", scale.DefaultYears, "fiscal years per (entity, obligation type), ending with the current one")
	asOf := fs.String("as-of", "", "civil date the status distribution is drawn for (default: today in --timezone)")
	fs.StringVar(&d.Config.Slug, "slug", scale.DefaultSlug, "tenant slug")
	fs.StringVar(&d.Config.Name, "name", scale.DefaultName, "tenant name")
	fs.StringVar(&d.Config.Timezone, "timezone", scale.DefaultTimezone, "tenant IANA timezone")
	fs.BoolVar(&d.Yes, "yes", false, "confirm writing the fixture (required)")
	fs.BoolVar(&d.DryRun, "dry-run", false, "generate and print the statistics, writing nothing")
	fs.StringVar(&d.OutputPath, "out", DefaultScaleOutputPath,
		"where the generation record (config, ids, counts) is written; verify --scale recomputes from the same config")
	fs.Usage = func() {
		fmt.Fprintln(d.Stderr, "usage: seed-demo scale --yes [flags]")
		fs.PrintDefaults()
	}
	// --as-of needs parsing after Parse; stash the pointer on the flag set
	// through a closure the caller invokes.
	d.parseAsOf = func() error {
		if *asOf == "" {
			return nil
		}
		parsed, err := dateonly.Parse(*asOf)
		if err != nil {
			return fmt.Errorf("--as-of must be a YYYY-MM-DD date, not %q", *asOf)
		}
		d.Config.AsOf = parsed
		return nil
	}
	return fs
}

// runScale is the subcommand.
func runScale(ctx context.Context, d ScaleDeps) error {
	if d.Stdout == nil {
		d.Stdout = io.Discard
	}
	if d.Stderr == nil {
		d.Stderr = io.Discard
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if len(d.Args) > 0 {
		fs := scaleFlagSet(&d)
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
		if err := d.parseAsOf(); err != nil {
			return err
		}
	}

	cfg := d.Config.WithDefaults()
	if cfg.AsOf.IsZero() {
		zone, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			return fmt.Errorf("load timezone %q: %w", cfg.Timezone, err)
		}
		cfg.AsOf = dateonly.FromTime(d.Now().In(zone))
	}

	started := d.Now()
	res, err := scale.Generate(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "scale fixture: tenant %s (%q, %s), seed %d, %d entities, %d years, asOf %s — generated in %s\n",
		cfg.Slug, cfg.Name, cfg.Timezone, cfg.Seed, cfg.Entities, cfg.Years, cfg.AsOf, d.Now().Sub(started).Round(time.Millisecond))
	printScaleStats(d.Stdout, res.Stats)

	if d.DryRun {
		fmt.Fprintln(d.Stdout, "\ndry run: nothing was written.")
		return nil
	}
	if !d.Yes {
		return errors.New("refusing to write the scale fixture without --yes (it inserts ~10⁵ rows)")
	}

	// The oracle's recomputation of the generated spec: the dates and
	// instants the rows carry. today is asOf — it only affects
	// classifications, which the writer does not persist.
	world, err := buildOracleTenant(res.Spec, *res.Tenant, d.Now(), cfg.AsOf)
	if err != nil {
		return fmt.Errorf("recompute the generated tenant: %w", err)
	}

	db := d.DB
	if db == nil {
		dbCfg, err := databaseConfig(d.DatabaseURL)
		if err != nil {
			return err
		}
		// A hundred thousand rows go only to a local database, checked on
		// the resolved DSN before any connection is opened (as reset does).
		if err := resetGuard(dbCfg.URL, os.Getenv); err != nil {
			return fmt.Errorf("the scale fixture (~10⁵ rows) is written to local databases only: %w", err)
		}
		conn, err := database.ConnectDB(&dbCfg)
		if err != nil {
			return fmt.Errorf("connect to the database: %w", err)
		}
		defer func() { _ = conn.Close() }()
		db = database.NewExec(conn)
	}

	if err := scalePreflight(ctx, db, res); err != nil {
		return err
	}

	counts, err := scaleWrite(ctx, db, res, world)
	if err != nil {
		return err
	}
	printScaleCounts(d.Stdout, res, counts)

	if d.OutputPath != "" {
		if err := writeScaleOutput(d.OutputPath, newScaleOutput(d.Now(), cfg, counts)); err != nil {
			return err
		}
		fmt.Fprintf(d.Stdout, "generation record written to %s (verify --scale recomputes from its config)\n", d.OutputPath)
	}
	return nil
}

// DefaultScaleOutputPath is where `scale` records how the fixture was
// generated.
const DefaultScaleOutputPath = "./seed-demo-scale.json"

// scaleOutput is the generation record: the Config the tenant is a function
// of — the only way to recompute the same world later — plus the ids and
// counts of what was written.
type scaleOutput struct {
	GeneratedAt string            `json:"generatedAt"`
	Config      scaleOutputConfig `json:"config"`
	TenantID    string            `json:"tenantId"`
	AdminID     string            `json:"adminId"`
	Counts      scaleOutputCounts `json:"counts"`
	Warning     string            `json:"warning"`
}

type scaleOutputConfig struct {
	Seed     int64  `json:"seed"`
	Entities int    `json:"entities"`
	Years    int    `json:"years"`
	AsOf     string `json:"asOf"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

type scaleOutputCounts struct {
	Users             int `json:"users"`
	Entities          int `json:"entities"`
	ObligationTypes   int `json:"obligationTypes"`
	EntityObligations int `json:"entityObligations"`
	Workflows         int `json:"workflows"`
	WorkflowTasks     int `json:"workflowTasks"`
	Instances         int `json:"instances"`
	ElapsedMs         int `json:"elapsedMs"`
}

func newScaleOutput(now time.Time, cfg scale.Config, c scaleCounts) scaleOutput {
	return scaleOutput{
		GeneratedAt: formatInstant(now),
		Config: scaleOutputConfig{
			Seed: cfg.Seed, Entities: cfg.Entities, Years: cfg.Years, AsOf: cfg.AsOf.String(),
			Slug: cfg.Slug, Name: cfg.Name, Timezone: cfg.Timezone,
		},
		TenantID: c.TenantID,
		AdminID:  c.AdminID,
		Counts: scaleOutputCounts{
			Users: c.Users, Entities: c.Entities, ObligationTypes: c.ObligationTypes,
			EntityObligations: c.EntityObligations, Workflows: c.Workflows, WorkflowTasks: c.WorkflowTasks,
			Instances: c.Instances, ElapsedMs: int(c.Elapsed / time.Millisecond),
		},
		Warning: "Generated fixture record. The tenant is a pure function of `config`: recompute " +
			"(verify --scale) from exactly these values — a different asOf draws different statuses.",
	}
}

// scaleConfigFromOutput restores the generator Config from a record.
func scaleConfigFromOutput(o scaleOutput) (scale.Config, error) {
	asOf, err := dateonly.Parse(o.Config.AsOf)
	if err != nil {
		return scale.Config{}, fmt.Errorf("generation record: asOf %q: %w", o.Config.AsOf, err)
	}
	return scale.Config{
		Seed: o.Config.Seed, Entities: o.Config.Entities, Years: o.Config.Years, AsOf: asOf,
		Slug: o.Config.Slug, Name: o.Config.Name, Timezone: o.Config.Timezone,
	}, nil
}

func writeScaleOutput(path string, out scaleOutput) error {
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the generation record: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write the generation record %s: %w", path, err)
	}
	return nil
}

// readScaleOutput reads a generation record (for a later `verify --scale`).
func readScaleOutput(path string) (scaleOutput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return scaleOutput{}, fmt.Errorf("read the generation record: %w", err)
	}
	var out scaleOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return scaleOutput{}, fmt.Errorf("decode the generation record %s: %w", path, err)
	}
	return out, nil
}

// scalePreflight refuses a BYPASSRLS connection (same reason as `seed`), an
// existing slug, and taken e-mails.
func scalePreflight(ctx context.Context, db platformseed.DB, res *scale.Result) error {
	if err := ensureRLSEnforced(ctx, db); err != nil {
		return err
	}
	tenant := res.Tenant
	var id string
	err := db.QueryRowxContext(ctx, `SELECT id FROM tenants WHERE slug = $1`, tenant.Slug).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("look up tenant %q: %w", tenant.Slug, err)
	default:
		return fmt.Errorf("tenant %q already exists as %s; remove it first with\n"+
			"  seed-demo reset --scale --yes --admin-dsn postgres://postgres:...@localhost:5433/zentax "+
			"--database-url <app DSN>", tenant.Slug, id)
	}

	emails := make([]string, 0, len(tenant.AllUsers()))
	for _, u := range tenant.AllUsers() {
		emails = append(emails, strings.ToLower(strings.TrimSpace(u.Email)))
	}
	taken, err := takenEmails(ctx, &deps{DB: db}, emails)
	if err != nil {
		return err
	}
	if len(taken) > 0 {
		sort.Strings(taken)
		return fmt.Errorf("these e-mail addresses are already registered (users.email is unique across tenants): %s\n"+
			"run `seed-demo reset --scale --yes --admin-dsn …` first, or pick another --slug",
			strings.Join(taken, ", "))
	}
	return nil
}

// printScaleStats prints the generator's statistics.
func printScaleStats(out io.Writer, s scale.Stats) {
	fmt.Fprintf(out, "  %d users, %d entities, %d obligation types, %d entity obligations\n",
		s.Users, s.Entities, s.ObligationTypes, s.EntityObligations)
	fmt.Fprintf(out, "  %d workflows (%d started: %s), %d task templates, %d task instances (%d listed as deviations)\n",
		s.Workflows, s.WorkflowsStarted, scaleTally(s.ByWorkflowStatus), s.WorkflowTasks, s.Instances, s.Deviations)
	fmt.Fprintf(out, "  instances by status:  %s\n", scaleTally(s.ByStatus))
	fmt.Fprintf(out, "  instances by band:    %s\n", scaleTally(s.ByBand))
	fmt.Fprintf(out, "  classification@asOf:  %s\n", scaleTally(s.Classification))
	fmt.Fprintf(out, "  dashboard@asOf:       overdue %d, due today %d, this week %d, awaiting approval %d, completed %d\n",
		s.Overdue, s.DueToday, s.ThisWeek, s.AwaitingApproval, s.Completed)
	fmt.Fprintf(out, "  assigned %d (%d of %d open), with tax data %d, with notes %d\n",
		s.Assigned, s.AssignedOpen, s.Open, s.WithTaxData, s.WithNotes)
}

// printScaleCounts prints what was written and how to sign in.
func printScaleCounts(out io.Writer, res *scale.Result, c scaleCounts) {
	fmt.Fprintf(out, "\nwritten in %s — tenant_id %s, admin %s\n", c.Elapsed.Round(time.Millisecond), c.TenantID, c.AdminID)
	fmt.Fprintf(out, "  %d users (%d grants), %d predefined data templates, %d entities, %d obligation types, %d entity obligations\n",
		c.Users, c.Grants, c.Templates, c.Entities, c.ObligationTypes, c.EntityObligations)
	fmt.Fprintf(out, "  %d workflows, %d workflow tasks, %d task instances\n", c.Workflows, c.WorkflowTasks, c.Instances)
	fmt.Fprintf(out, "\nsign in: %s / %s (tenant %s; members share %s)\n",
		res.Tenant.Admin.Email, res.Tenant.Admin.Password, res.Tenant.Slug, scale.MemberPassword)
	fmt.Fprintln(out, "audit_log was not written: it is append-only and hash-chained (ADR-0007/0008); "+
		"the fixture's history has no trail. Documents were not written either.")
}

// scaleTally renders a map as "a 1, b 2" in key order.
func scaleTally(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
