package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	platformseed "github.com/mohamadhallal/zentax-api/platform/seed"
)

// `seed-demo reset` deletes the dataset's tenants — and nothing else. It is
// what makes the seeder re-runnable: seed, look, reset, seed again.
//
// Two locks stand in front of it, because a tenant delete is irreversible and
// this tool's whole job is to be pointed at a database:
//
//  1. --yes. The command prints what it would delete and stops without it.
//  2. A LOCAL-DATABASE guard. The DSN's host must be a loopback address (or the
//     process must be running with APP_ENV=development, which is how the
//     compose stack runs, where the host is the "postgres" service name). A
//     staging or production endpoint is refused outright: no flag overrides it.
//
// Only the slugs that appear in the dataset are touched, so a reset cannot take
// a neighbouring tenant with it (the compose stack's own acme / admin@acme.test
// tenant, for instance — which is why the dataset's acme slug is "acme-demo").
//
// A note on privileges: platform/seed.DeleteTenant has to clear audit_log,
// which is append-only for the application role (the migrate job REVOKEs
// UPDATE/DELETE and the RLS policies allow only SELECT/INSERT). In the compose
// stack that means the postgres superuser DSN, not zentax_app — pass it with
// --database-url. The error says so when the grant is missing.

// developmentEnv is the APP_ENV value that marks a throwaway environment.
const developmentEnv = "development"

// unknownHost is what databaseHost reports when the DSN names no host it can
// read. It is deliberately NOT a member of localHosts: "I could not tell" must
// never be treated as "it is local".
const unknownHost = "unknown"

// localHosts are the database hosts a destructive command accepts without an
// APP_ENV=development declaration.
var localHosts = map[string]bool{
	"":          true, // a Unix socket / no host: local by construction
	"localhost": true,
	"127.0.0.1": true,
	"::1":       true,
	"[::1]":     true,
}

// runReset deletes the selected tenants.
func runReset(ctx context.Context, d *deps) error {
	if len(d.Tenants) == 0 {
		return errors.New("no tenants selected")
	}

	fmt.Fprintf(d.Err, "reset will delete %d tenant(s) and everything in them "+
		"(entities, workflows, task instances, documents, users, audit log):\n", len(d.Tenants))
	for _, tenant := range d.Tenants {
		fmt.Fprintf(d.Err, "  - %s (%s)\n", tenant.Slug, tenant.Key)
	}

	// The delete travels over --admin-dsn when one was given, because clearing
	// audit_log needs a role the application role deliberately is not.
	db, dsn := d.DB, d.DatabaseURL
	if d.AdminDB != nil {
		db, dsn = d.AdminDB, d.AdminDatabaseURL
	}

	if err := resetGuard(dsn, os.Getenv); err != nil {
		return err
	}
	if !d.Yes {
		return errors.New("refusing to delete without --yes")
	}

	for _, tenant := range d.Tenants {
		err := platformseed.DeleteTenant(ctx, db, tenant.Slug)
		switch {
		case errors.Is(err, platformseed.ErrTenantNotFound):
			fmt.Fprintf(d.Out, "  %s: not present, nothing to delete\n", tenant.Slug)
		case err != nil:
			return err
		default:
			fmt.Fprintf(d.Out, "  %s: deleted\n", tenant.Slug)
		}
	}
	return nil
}

// resetGuard refuses a destructive run against anything but a local database.
// getenv is injected so the rule is testable without touching the process
// environment.
func resetGuard(databaseURL string, getenv func(string) string) error {
	if getenv("APP_ENV") == developmentEnv {
		return nil
	}
	host := databaseHost(databaseURL)
	if localHosts[strings.ToLower(host)] {
		return nil
	}
	where := "host " + host
	if host == unknownHost {
		where = "a DSN with no recognisable host"
	}
	return fmt.Errorf("refusing to delete tenants: the database is at %s, which is not local. "+
		"`seed-demo reset` only runs against a database on localhost / 127.0.0.1, or with APP_ENV=%s set "+
		"(the compose stack). Point --database-url at the demo database", where, developmentEnv)
}

// databaseHost extracts the host from either DSN form Postgres accepts: a URL
// ("postgres://user:pass@host:5433/db") or a keyword string ("host=… port=…").
// A URL that names no host ("postgres:///zentax") yields "", the local socket;
// anything it cannot read yields unknownHost, which the guard refuses.
func databaseHost(databaseURL string) string {
	trimmed := strings.TrimSpace(databaseURL)
	if trimmed == "" {
		// No DSN at all: config.Load() supplied it, and the safe reading is
		// "unknown", not "local".
		return unknownHost
	}
	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return unknownHost
		}
		return parsed.Hostname()
	}
	// Keyword/value DSN: host=… may be quoted.
	for _, field := range strings.Fields(trimmed) {
		key, value, found := strings.Cut(field, "=")
		if found && strings.EqualFold(strings.TrimSpace(key), "host") {
			return strings.Trim(strings.TrimSpace(value), `'"`)
		}
	}
	return unknownHost
}
