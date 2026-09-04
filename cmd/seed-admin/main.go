// Command seed-admin bootstraps a tenant and its first admin user (ADR-0011).
// There is no public signup yet, so this is how a first user is provisioned:
//
//	APP_ENV=development go run ./cmd/seed-admin \
//	  --tenant-slug acme --tenant-name "Acme GmbH" \
//	  --email admin@acme.com --password 's3cret' --name "Group Head of Tax" \
//	  --timezone Europe/London
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // embed the IANA zone database: the runtime image is bare alpine (ADR-0003)

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/logger"
	datatemplatespg "github.com/mohamadhallal/zentax-api/modules/datatemplates/repositories/pg"
	datatemplatesusecases "github.com/mohamadhallal/zentax-api/modules/datatemplates/usecases"
	identitydomain "github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

func main() {
	tenantSlug := flag.String("tenant-slug", "", "tenant slug (unique)")
	tenantName := flag.String("tenant-name", "", "tenant display name")
	email := flag.String("email", "", "admin email")
	password := flag.String("password", "", "admin password")
	name := flag.String("name", "Admin", "admin display name")
	timezone := flag.String("timezone", identitydomain.DefaultTimezone, "tenant IANA timezone (ADR-0003), e.g. Europe/London")
	flag.Parse()

	if *tenantSlug == "" || *tenantName == "" || *email == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "usage: seed-admin --tenant-slug S --tenant-name N --email E --password P [--name Name] [--timezone Zone]")
		os.Exit(1)
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

	ctx := context.Background()
	hash, err := crypto.HashPassword(*password)
	if err != nil {
		fail("hash password", err)
	}

	// tenants + users are not RLS-scoped.
	var tenantID string
	if err := db.QueryRowxContext(ctx,
		`INSERT INTO tenants (slug, name, timezone) VALUES ($1, $2, $3) RETURNING id`,
		*tenantSlug, *tenantName, *timezone).Scan(&tenantID); err != nil {
		fail("create tenant", err)
	}

	var userID string
	if err := db.QueryRowxContext(ctx,
		`INSERT INTO users (tenant_id, email, name, password_hash, status)
		 VALUES ($1, $2, $3, $4, 'active') RETURNING id`,
		tenantID, strings.ToLower(strings.TrimSpace(*email)), *name, hash).Scan(&userID); err != nil {
		fail("create admin user", err)
	}

	// user_grants is RLS-scoped: set the tenant GUC (ADR-0004) for the insert.
	if err := db.WithinTransaction(app.WithTenantID(ctx, tenantID), func(txCtx context.Context) error {
		_, e := db.ExecContext(txCtx,
			`INSERT INTO user_grants (user_id, role) VALUES ($1, 'tenant_admin')`, userID)
		return e
	}); err != nil {
		fail("create tenant-admin grant", err)
	}

	// Predefined data templates (VAT / CIT / WHT): the same idempotent use case
	// POST /data-templates/predefined runs, on a tenant-bound tx with the admin
	// as the acting user (created_by). No authorizer / audit here — there is no
	// request; the seeding is attributed through created_by.
	seedCtx := app.WithRequester(app.WithTenantID(ctx, tenantID), &app.Requester{Kind: app.RequesterUser, ID: userID})
	var seeded int
	if err := db.WithinTransaction(seedCtx, func(txCtx context.Context) error {
		templates, e := datatemplatesusecases.NewUseCases(datatemplatespg.NewDataTemplateRepo(db)).SeedPredefined(txCtx)
		seeded = len(templates)
		return e
	}); err != nil {
		fail("seed predefined data templates", err)
	}

	fmt.Printf("Seeded tenant %q\n  tenant_id: %s\n  timezone:  %s\n  user_id:   %s\n  admin:     %s\n  templates: %d predefined\n",
		*tenantName, tenantID, *timezone, userID, *email, seeded)
}

func fail(msg string, err error) {
	fmt.Fprintf(os.Stderr, "seed-admin: %s: %v\n", msg, err)
	os.Exit(1)
}
