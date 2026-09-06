// Command seed-admin bootstraps a tenant and its first admin user (ADR-0011).
// There is no public signup yet, so this is how a first user is provisioned:
//
//	APP_ENV=development SEED_ADMIN_PASSWORD='correct-horse-battery' go run ./cmd/seed-admin \
//	  --tenant-slug acme --tenant-name "Acme GmbH" \
//	  --email admin@acme.com --name "Group Head of Tax" \
//	  --timezone Europe/London
//
// The password comes from SEED_ADMIN_PASSWORD (preferred — in deployed
// environments ECS injects it from Secrets Manager, so it never lands in a
// task definition or CloudTrail) or from --password for local use. The tool
// refuses to run when neither is set and never prints the password.
//
// The provisioning itself lives in platform/seed, so the demo seeder creates
// its tenants exactly the same way; this file is flags, config and output.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
	_ "time/tzdata" // embed the IANA zone database: the runtime image is bare alpine (ADR-0003)

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/logger"
	identitydomain "github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/seed"
)

func main() {
	tenantSlug := flag.String("tenant-slug", "", "tenant slug (unique)")
	tenantName := flag.String("tenant-name", "", "tenant display name")
	email := flag.String("email", "", "admin email")
	passwordFlag := flag.String("password", "", "admin password (local use; in deployed environments prefer the "+passwordEnv+
		" environment variable — ECS injects it from Secrets Manager, so it never appears in CloudTrail or task definitions)")
	name := flag.String("name", "Admin", "admin display name")
	timezone := flag.String("timezone", identitydomain.DefaultTimezone, "tenant IANA timezone (ADR-0003), e.g. Europe/London")
	flag.Parse()

	if *tenantSlug == "" || *tenantName == "" || *email == "" {
		fmt.Fprintln(os.Stderr, "usage: "+passwordEnv+"=... seed-admin --tenant-slug S --tenant-name N --email E [--password P] [--name Name] [--timezone Zone]")
		os.Exit(1)
	}
	password, err := resolvePassword(*passwordFlag, os.Getenv)
	if err != nil {
		fail("resolve password", err)
	}
	// The same rule PUT /tenant applies: a loadable IANA zone, never "Local".
	if err := identitydomain.ValidateTimezone(*timezone, time.LoadLocation); err != nil {
		fail("validate --timezone", err)
	}

	logger.InitBasic()

	cfg, err := config.Load()
	if err != nil {
		fail("load config", err)
	}
	dbConn, err := database.ConnectDB(&cfg.Database)
	if err != nil {
		fail("connect db", err)
	}
	defer func() { _ = dbConn.Close() }()
	db := database.NewExec(dbConn)

	ids, err := seed.CreateTenant(context.Background(), db, seed.Params{
		Slug:     *tenantSlug,
		Name:     *tenantName,
		Email:    *email,
		Password: password,
		UserName: *name,
		Timezone: *timezone,
	})
	if err != nil {
		// CreateTenant names the stage that failed ("create tenant: …"), so the
		// line printed here is the one this tool has always printed.
		failErr(err)
	}

	// Reference ids only (ADR-0015): this line ends up in CI / task logs, so the
	// admin's e-mail address is deliberately not echoed.
	fmt.Printf("Seeded tenant %q\n  tenant_id: %s\n  timezone:  %s\n  admin user_id: %s\n  templates: %d predefined\n",
		*tenantSlug, ids.TenantID, ids.Timezone, ids.UserID, ids.Templates)
}

func fail(msg string, err error) {
	fmt.Fprintf(os.Stderr, "seed-admin: %s: %v\n", msg, err)
	os.Exit(1)
}

// failErr reports an error that already carries its own stage prefix.
func failErr(err error) {
	fmt.Fprintf(os.Stderr, "seed-admin: %v\n", err)
	os.Exit(1)
}
