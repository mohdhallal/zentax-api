// Command seed-demo materializes the demo dataset (seed/demo/dataset.json) in a
// running ZenTax stack, verifies it, and removes it again.
//
//	seed-demo seed   [--spec F] [--api URL] [--database-url DSN] [--out F] [--only KEYS] [--dry-run] [--reset --admin-dsn DSN --yes]
//	seed-demo verify [--spec F] [--api URL] [--in F] [--only KEYS] [--today D] [--json] [--strict-static]
//	seed-demo reset  [--spec F] [--database-url DSN] [--admin-dsn DSN] [--only KEYS | --scale] --yes
//	seed-demo scale  [--database-url DSN] [--seed N] [--entities N] [--years N] [--as-of D] [--dry-run] --yes
//	seed-demo bench  [--api URL] [--tenant SLUG] [--n N] [--warmup N] [--page-size N] [--deep-page N] [--json] [--out F] [--no-fail]
//
// scale and bench belong to the scale fixture (seed/demo/scale): a generated
// 10⁵-instance tenant and the ADR-0021 rule 7 measurement over it.
//
// The two DSNs are not interchangeable in either direction. --database-url is
// the APPLICATION role: the tenant bootstrap's idempotency checks are scoped by
// row-level security (ADR-0004), so a BYPASSRLS connection silently seeds no
// data templates — `seed` refuses one. --admin-dsn is the privileged role only
// `reset` needs, because deleting a tenant clears the append-only audit_log.
//
// The dataset is the contract: it declares three tenants, their people, the
// whole workflow chain, and the numbers every report must return for them. This
// command creates that world the way a real operator would — HTTP calls made as
// the seeded users, in the dataset's own order — with exactly two direct-SQL
// steps that no endpoint exposes:
//
//  1. the tenant, its admin and the predefined data templates (there is no
//     tenant endpoint; platform/seed does it, exactly as cmd/seed-admin does);
//  2. the completion instants (completed_at / submitted_at / approved_at are
//     Postgres NOW() everywhere in the API, so a demo whose completions are
//     dated last March has to back-date them once, at the end).
//
// audit_log is never touched: it is append-only and hash-chained (ADR-0007 /
// ADR-0008), so the trail legitimately shows the seed run's own instants.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed the IANA zone database: completion instants are computed in the tenant's zone (ADR-0003)

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/database"
	platformseed "github.com/mohamadhallal/zentax-api/platform/seed"
	"github.com/mohamadhallal/zentax-api/seed/demo/scale"
	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/apiclient"
)

const (
	// DefaultSpecPath is the dataset, relative to the repository root (the
	// directory this command is normally run from — config.Load() reads
	// deployment/config_files/ from the working directory too).
	DefaultSpecPath = "seed/demo/dataset.json"
	// DefaultAPIBaseURL is the compose stack's API. There is no /api prefix on
	// the Go side; that belongs to the UI's Express proxy.
	DefaultAPIBaseURL = "http://localhost:3000"
	// DefaultOutputPath is where the key → id map is written.
	DefaultOutputPath = "./seed-demo-output.json"
)

const usage = `seed-demo — build, check and remove the ZenTax demo dataset.

usage:
  seed-demo seed   [flags]      create every tenant in the dataset
  seed-demo verify [flags]      recompute the expected reports and diff them against the live API
  seed-demo reset  [flags] --yes   delete the dataset's tenants (local databases only)
  seed-demo scale  [flags] --yes   generate and bulk-write the scale fixture tenant (seed/demo/scale)
  seed-demo bench  [flags]         measure the ADR-0021 rule 7 targets against a tenant

verify, scale and bench parse their own arguments; run "seed-demo <cmd> -h"
for those. The flags below belong to seed and reset:
`

// deps is everything a subcommand needs. verify.go (owned by the verifier)
// receives one of these; nothing in it is specific to seeding.
type deps struct {
	// Spec is the loaded, validated dataset.
	Spec *spec.Spec
	// SpecPath is where it came from (for messages).
	SpecPath string
	// API is an anonymous client for the target API; sessions are derived from
	// it with Login / WithSession.
	API *apiclient.Client
	// DB is the database connection, or nil in --dry-run. It is opened as the
	// APPLICATION role: the tenant bootstrap depends on row-level security to
	// scope its idempotency checks, so seeding over a BYPASSRLS connection is
	// silently wrong (see ensureRLSEnforced).
	DB platformseed.DB
	// DatabaseURL is the DSN DB was opened with ("" in --dry-run).
	DatabaseURL string
	// AdminDB is the privileged connection `reset` deletes through, or nil when
	// --admin-dsn was not given (then DB is used). A tenant delete has to clear
	// audit_log, which is append-only for the application role.
	AdminDB platformseed.DB
	// AdminDatabaseURL is the DSN AdminDB was opened with ("" when unused).
	AdminDatabaseURL string
	// OutputPath is the machine-readable key → id map seed writes and verify
	// reads.
	OutputPath string
	// Tenants are the dataset tenants this run acts on, in dataset order
	// (every tenant unless --only narrowed it).
	Tenants []spec.Tenant
	// DryRun plans without writing anything.
	DryRun bool
	// Yes confirms a destructive operation.
	Yes bool
	// Reset asks `seed` to delete the dataset's tenants first.
	Reset bool
	// Out and Err are where human output goes.
	Out io.Writer
	Err io.Writer
	// Now is the clock (injected so output is testable).
	Now func() time.Time
}

func main() {
	logger.InitBasic()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "seed-demo: %v\n", err)
		os.Exit(1)
	}
}

// run is main without the process: it parses the command line, assembles deps
// and dispatches. Every error it returns is already scoped to what failed.
func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return errors.New("a subcommand is required: seed | verify | reset")
	}
	sub := args[0]
	switch sub {
	case "seed", "verify", "reset", "scale", "bench":
	case "-h", "--help", "help":
		fmt.Fprint(out, usage)
		return nil
	default:
		fmt.Fprint(errOut, usage)
		return fmt.Errorf("unknown subcommand %q (want seed, verify, reset, scale or bench)", sub)
	}

	// The scale fixture's two subcommands parse their own arguments too.
	switch sub {
	case "scale":
		return runScale(ctx, ScaleDeps{Stdout: out, Stderr: errOut, Now: time.Now, Args: args[1:]})
	case "bench":
		return runBench(ctx, BenchDeps{Stdout: out, Stderr: errOut, Now: time.Now, Args: args[1:]})
	}

	// `verify` parses its own arguments: it carries three options this flag set
	// does not (--today / --json / --strict-static), and it needs no database
	// — it resolves the dataset's keys from the live tenant. Everything below
	// this point is seed's and reset's.
	if sub == "verify" {
		return runVerify(ctx, VerifyDeps{
			Spec:   DefaultSpecPath,
			API:    DefaultAPIBaseURL,
			In:     DefaultOutputPath,
			Stdout: out,
			Stderr: errOut,
			Now:    time.Now,
			Args:   args[1:],
		})
	}

	fs := flag.NewFlagSet("seed-demo "+sub, flag.ContinueOnError)
	fs.SetOutput(errOut)
	specPath := fs.String("spec", DefaultSpecPath, "path to the demo dataset")
	apiURL := fs.String("api", DefaultAPIBaseURL, "API base URL (no /api prefix — that is the UI proxy's)")
	dbURL := fs.String("database-url", "", "database DSN for the APPLICATION role (default: config.Load(), i.e. DATABASE_URL or\n"+
		"\tdeployment/config_files/$APP_ENV.json). It must NOT bypass RLS: the tenant bootstrap is\n"+
		"\tscoped by row-level security, so seeding as a superuser silently creates no data templates")
	adminDSN := fs.String("admin-dsn", "", "privileged DSN used ONLY to delete tenants (reset, and seed --reset). A tenant delete\n"+
		"\tclears audit_log, which is append-only for the application role — in the compose stack\n"+
		"\tpostgres://postgres:...@localhost:5433/zentax. Defaults to --database-url")
	outPath := fs.String("out", DefaultOutputPath, "where seed writes the key → id map (verify reads it with its own --in)")
	only := fs.String("only", "", "comma-separated tenant keys or slugs to act on (default: every tenant in the dataset)")
	dryRun := fs.Bool("dry-run", false, "seed only: validate the dataset and print the plan, writing nothing")
	yes := fs.Bool("yes", false, "confirm a destructive operation (required by reset)")
	reset := fs.Bool("reset", false, "seed only: delete the dataset's tenants first (needs --yes)")
	scaleOnly := fs.Bool("scale", false, "reset only: delete the scale fixture tenant (see --scale-slug) instead of the dataset's tenants")
	scaleSlug := fs.String("scale-slug", scale.DefaultSlug, "reset --scale: the scale fixture's tenant slug")
	fs.Usage = func() {
		fmt.Fprint(errOut, usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if sub != "seed" && (*dryRun || *reset) {
		return fmt.Errorf("--dry-run and --reset apply to `seed-demo seed`, not to %q", sub)
	}
	if sub != "reset" && *scaleOnly {
		return fmt.Errorf("--scale applies to `seed-demo reset`, not to %q", sub)
	}

	var (
		dataset *spec.Spec
		tenants []spec.Tenant
	)
	if *scaleOnly {
		// The scale fixture is not in the dataset: reset addresses it by slug
		// alone, never together with the dataset's tenants, and does not need
		// the dataset file at all.
		if strings.TrimSpace(*only) != "" {
			return errors.New("--scale and --only do not combine: reset either the scale fixture or dataset tenants")
		}
		tenants = []spec.Tenant{{Key: *scaleSlug, Slug: *scaleSlug}}
	} else {
		var err error
		if dataset, err = spec.LoadAndValidate(*specPath); err != nil {
			return err
		}
		if tenants, err = selectTenants(dataset, *only); err != nil {
			return err
		}
	}

	d := &deps{
		Spec:       dataset,
		SpecPath:   *specPath,
		API:        apiclient.New(*apiURL),
		OutputPath: *outPath,
		Tenants:    tenants,
		DryRun:     *dryRun,
		Yes:        *yes,
		Reset:      *reset,
		Out:        out,
		Err:        errOut,
		Now:        time.Now,
	}

	// A dry run touches nothing: no database, no API. Everything else needs a
	// connection, because both the tenant bootstrap and the completion instants
	// live below the HTTP surface.
	if !(sub == "seed" && *dryRun) {
		dbCfg, err := databaseConfig(*dbURL)
		if err != nil {
			return err
		}
		d.DatabaseURL = dbCfg.URL

		// The destructive guard runs on the RESOLVED DSN and BEFORE any
		// connection is opened. Connecting first would dial a production
		// database (and offer it credentials) before anything decided it was
		// off limits, and an unreachable remote host would report a DNS
		// failure where the honest answer is "that database is not local".
		// Both DSNs are checked: whichever one the delete travels over has to
		// be local.
		if sub == "reset" || *reset {
			if err := resetGuard(d.DatabaseURL, os.Getenv); err != nil {
				return err
			}
			if *adminDSN != "" {
				if err := resetGuard(*adminDSN, os.Getenv); err != nil {
					return err
				}
			}
		}

		conn, err := database.ConnectDB(&dbCfg)
		if err != nil {
			return fmt.Errorf("connect to the database: %w", err)
		}
		defer func() { _ = conn.Close() }()
		d.DB = database.NewExec(conn)

		// The privileged connection is opened only when it is actually going
		// to be used, so a plain `seed` never needs the credential at all.
		if *adminDSN != "" && (sub == "reset" || *reset) {
			adminCfg := dbCfg
			adminCfg.URL = *adminDSN
			adminConn, err := database.ConnectDB(&adminCfg)
			if err != nil {
				return fmt.Errorf("connect to the database with --admin-dsn: %w", err)
			}
			defer func() { _ = adminConn.Close() }()
			d.AdminDB = database.NewExec(adminConn)
			d.AdminDatabaseURL = adminCfg.URL
		}
	}

	if sub == "seed" {
		return runSeed(ctx, d)
	}
	return runReset(ctx, d)
}

// selectTenants narrows the dataset to --only, matching a tenant by KEY or by
// SLUG (they differ: the acme tenant's slug is acme-demo). Order is always the
// dataset's, never the flag's, so two runs produce the same data.
func selectTenants(dataset *spec.Spec, only string) ([]spec.Tenant, error) {
	if strings.TrimSpace(only) == "" {
		return dataset.Tenants, nil
	}
	wanted := map[string]bool{}
	for _, name := range strings.Split(only, ",") {
		if name = strings.TrimSpace(name); name != "" {
			wanted[name] = true
		}
	}
	if len(wanted) == 0 {
		return nil, errors.New("--only was given but names no tenant")
	}

	var selected []spec.Tenant
	matched := map[string]bool{}
	for _, tenant := range dataset.Tenants {
		switch {
		case wanted[tenant.Key]:
			matched[tenant.Key] = true
		case wanted[tenant.Slug]:
			matched[tenant.Slug] = true
		default:
			continue
		}
		selected = append(selected, tenant)
	}
	var unknown []string
	for name := range wanted {
		if !matched[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("--only names %s, which is neither a tenant key nor a slug in the dataset (have: %s)",
			strings.Join(unknown, ", "), tenantNames(dataset))
	}
	return selected, nil
}

func tenantNames(dataset *spec.Spec) string {
	names := make([]string, 0, len(dataset.Tenants))
	for _, tenant := range dataset.Tenants {
		names = append(names, tenant.Key+" ("+tenant.Slug+")")
	}
	return strings.Join(names, ", ")
}

// databaseConfig resolves the DSN and the pool settings: the config file when
// it loads (the seed-admin path), the flag alone otherwise, so the tool works
// from a directory that has no deployment/config_files.
func databaseConfig(flagURL string) (config.DatabaseConfig, error) {
	cfg, err := config.Load()
	if err == nil {
		dbCfg := cfg.Database
		if flagURL != "" {
			dbCfg.URL = flagURL
		}
		return dbCfg, nil
	}
	if flagURL == "" {
		return config.DatabaseConfig{}, fmt.Errorf("resolve the database DSN (pass --database-url to skip the config file): %w", err)
	}
	return config.DatabaseConfig{
		URL:                 flagURL,
		PoolMax:             4,
		IdleTimeoutMs:       30_000,
		ConnectionTimeoutMs: 10_000,
		ConnMaxLifetimeMs:   300_000,
	}, nil
}
