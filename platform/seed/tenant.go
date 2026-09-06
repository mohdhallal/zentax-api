// Package seed provisions a tenant and its first administrator, and removes
// one again. It is the behaviour cmd/seed-admin used to carry inline, lifted
// into a package so the demo seeder (seed/demo) creates its tenants exactly the
// way the supported bootstrap path does — one implementation, not two that
// drift.
//
// Everything here is a direct-to-database bootstrap: there is no public signup
// and no HTTP request behind it (ADR-0011). Writes that touch RLS-scoped tables
// run inside a transaction bound to the new tenant (ADR-0004).
package seed

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	datatemplatespg "github.com/mohamadhallal/zentax-api/modules/datatemplates/repositories/pg"
	datatemplatesusecases "github.com/mohamadhallal/zentax-api/modules/datatemplates/usecases"
	identitydomain "github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// DB is the database seam this package needs: queries plus a tenant-bound
// transaction. *database.Exec satisfies it.
type DB interface {
	database.ExecerPg
	database.ExecerPgTx
}

// Params describes the tenant and the administrator to create with it.
type Params struct {
	// Slug is the tenant's unique short name (tenants.slug).
	Slug string
	// Name is the tenant's display name.
	Name string
	// Email is the admin's login address; it is lowercased and trimmed, and
	// must be unique across ALL tenants (users.email is globally unique).
	Email string
	// Password is the admin's cleartext password. It is hashed here (argon2id)
	// and never stored, logged or returned. Length policy is the caller's:
	// cmd/seed-admin enforces the same 12..200 rule POST /auth/accept-invite
	// applies.
	Password string
	// UserName is the admin's display name; defaults to "Admin".
	UserName string
	// Timezone is the tenant's IANA zone (ADR-0003); defaults to UTC.
	Timezone string
}

// TenantIDs is what CreateTenant produced. Reference ids only (ADR-0015): a
// caller may print these into a CI or task log, an e-mail address may not.
type TenantIDs struct {
	TenantID  string
	UserID    string
	Timezone  string // the zone actually applied (Params.Timezone, or the default)
	Templates int    // predefined data templates seeded for the tenant
}

// CreateTenant inserts the tenant, its first (active) administrator with a
// tenant-wide tenant_admin grant, and the predefined data templates.
//
// The steps and their order are the ones cmd/seed-admin has always performed:
//
//  1. hash the password (argon2id);
//  2. INSERT INTO tenants (slug, name, timezone) — control-plane metadata, not
//     RLS-scoped;
//  3. INSERT INTO users (…, status 'active') — also not RLS-scoped, because
//     login must find a user before any tenant context exists;
//  4. INSERT INTO user_grants (user_id, 'tenant_admin') inside a transaction
//     that binds app.tenant_id (user_grants IS RLS-scoped);
//  5. SeedPredefined — the same idempotent use case POST /data-templates/
//     predefined runs, on a tenant-bound transaction with the admin as the
//     acting user so created_by is attributed.
//
// Errors are wrapped with the stage that failed ("create tenant: …") and never
// echo the password.
func CreateTenant(ctx context.Context, db DB, params Params) (TenantIDs, error) {
	if db == nil {
		return TenantIDs{}, errors.New("seed: nil database")
	}
	params, err := params.normalized()
	if err != nil {
		return TenantIDs{}, err
	}

	hash, err := crypto.HashPassword(params.Password)
	if err != nil {
		return TenantIDs{}, fmt.Errorf("hash password: %w", err)
	}

	// tenants + users are not RLS-scoped.
	var tenantID string
	if err := db.QueryRowxContext(ctx,
		`INSERT INTO tenants (slug, name, timezone) VALUES ($1, $2, $3) RETURNING id`,
		params.Slug, params.Name, params.Timezone).Scan(&tenantID); err != nil {
		return TenantIDs{}, fmt.Errorf("create tenant: %w", err)
	}

	var userID string
	if err := db.QueryRowxContext(ctx,
		`INSERT INTO users (tenant_id, email, name, password_hash, status)
		 VALUES ($1, $2, $3, $4, 'active') RETURNING id`,
		tenantID, params.Email, params.UserName, hash).Scan(&userID); err != nil {
		return TenantIDs{}, fmt.Errorf("create admin user: %w", err)
	}

	// user_grants is RLS-scoped: set the tenant GUC (ADR-0004) for the insert.
	if err := db.WithinTransaction(app.WithTenantID(ctx, tenantID), func(txCtx context.Context) error {
		_, e := db.ExecContext(txCtx,
			`INSERT INTO user_grants (user_id, role) VALUES ($1, 'tenant_admin')`, userID)
		return e
	}); err != nil {
		return TenantIDs{}, fmt.Errorf("create tenant-admin grant: %w", err)
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
		return TenantIDs{}, fmt.Errorf("seed predefined data templates: %w", err)
	}

	return TenantIDs{
		TenantID:  tenantID,
		UserID:    userID,
		Timezone:  params.Timezone,
		Templates: seeded,
	}, nil
}

// normalized applies the defaults and the checks that do not need a database:
// required fields, and a loadable IANA zone (the same rule PUT /tenant applies,
// never "Local").
func (p Params) normalized() (Params, error) {
	p.Slug = strings.TrimSpace(p.Slug)
	p.Name = strings.TrimSpace(p.Name)
	p.Email = strings.ToLower(strings.TrimSpace(p.Email))
	p.UserName = strings.TrimSpace(p.UserName)

	switch {
	case p.Slug == "":
		return p, errors.New("tenant slug is required")
	case p.Name == "":
		return p, errors.New("tenant name is required")
	case p.Email == "":
		return p, errors.New("admin email is required")
	case p.Password == "":
		return p, errors.New("admin password is required")
	}

	if p.UserName == "" {
		p.UserName = "Admin"
	}
	if p.Timezone == "" {
		p.Timezone = identitydomain.DefaultTimezone
	}
	if err := identitydomain.ValidateTimezone(p.Timezone, time.LoadLocation); err != nil {
		return p, fmt.Errorf("validate timezone: %w", err)
	}
	return p, nil
}
