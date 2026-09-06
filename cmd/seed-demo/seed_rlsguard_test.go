package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Seeding over a connection that bypasses row-level security does not fail
// loudly — it fails QUIETLY, three stages later.
//
// platform/seed.CreateTenant finishes by seeding the predefined data templates
// through the same use case POST /data-templates/predefined runs, and that use
// case decides what is missing with
//
//	uc.repo.List(ctx, domain.ListDataTemplatesArgs{})
//
// which carries no tenant predicate: it is scoped by RLS alone. That is correct
// for the API, whose role is NOBYPASSRLS (ADR-0004). On a superuser connection
// the same list returns EVERY tenant's templates, so if any other tenant
// already owns the name "VAT Return", the new tenant is judged to have it, no
// row is inserted, and the closing List hands back the other tenant's rows.
// CreateTenant then reports "3 predefined data templates" for a tenant that has
// none.
//
// This was observed against the compose stack on 2026-09-06: seeding with the
// postgres superuser DSN while the stack's original `acme` tenant still held
// the three predefined names produced a tenant with 0 templates, a cheerful
// "3 predefined data templates" line, and a failure much later and far away —
// "workflow acme.w1: task template prepare: the tenant has no predefined VAT
// data template (have: )" — with five entities and six users already created.
//
// So the guard refuses up front, and these cases pin when.
func TestRLSGuardRefusesConnectionsThatBypassRLS(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		superuser bool
		bypassRLS bool
		wantWhy   string
	}{
		{
			name: "the application role is what seeding must use",
			role: "zentax_app",
		},
		{
			name:      "a superuser bypasses every policy",
			role:      "postgres",
			superuser: true,
			wantWhy:   "is a superuser",
		},
		{
			// Rarer and easier to miss: not a superuser, but exempt from RLS
			// all the same, which is the property that actually matters here.
			name:      "BYPASSRLS alone is equally unsafe",
			role:      "zentax_ops",
			bypassRLS: true,
			wantWhy:   "has BYPASSRLS",
		},
		{
			name:      "both flags still names the superuser reason",
			role:      "postgres",
			superuser: true,
			bypassRLS: true,
			wantWhy:   "is a superuser",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := rlsGuardError(tc.role, tc.superuser, tc.bypassRLS)
			if tc.wantWhy == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			// The message has to name the role, say what is wrong with it, and
			// hand over the DSN that works — an operator reading this is
			// holding the wrong credential and needs the right one.
			assert.Contains(t, err.Error(), tc.role)
			assert.Contains(t, err.Error(), tc.wantWhy)
			assert.Contains(t, err.Error(), "zentax_app")
			assert.Contains(t, err.Error(), "--admin-dsn")
		})
	}
}
