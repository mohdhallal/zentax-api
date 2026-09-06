package seed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// ErrTenantNotFound is returned by DeleteTenant when no tenant carries the slug.
var ErrTenantNotFound = errors.New("tenant not found")

// pgInsufficientPrivilege is SQLSTATE 42501 — "permission denied for table".
const pgInsufficientPrivilege = "42501"

// tenantTables is every table holding tenant-scoped rows, ordered children
// first. DeleteTenant walks it before removing the tenants row.
//
// Why not simply DELETE FROM tenants and let ON DELETE CASCADE do the work?
// Two constraints in the schema make that unreliable:
//
//   - audit_log references tenants ON DELETE *RESTRICT* (the ledger is not
//     collateral damage of a tenant delete, ADR-0007/0008), so the tenants row
//     cannot go while audit rows remain.
//   - workflows and entity_obligations reference obligation_types ON DELETE
//     RESTRICT. A cascade from tenants deletes those three tables in an order
//     Postgres picks (constraint creation order), and RESTRICT is checked
//     immediately — so obligation_types can be cascaded away while workflows
//     still reference it, and the whole delete fails.
//
// Deleting explicitly, children first, inside one transaction is deterministic
// and does not depend on the order the FK triggers happen to fire.
var tenantTables = []string{
	// documents → workflows / task_instances
	"document_versions",
	"documents",
	// the workflow chain
	"task_instances",
	"workflow_tasks",
	"workflows",
	// obligations: both referrers of obligation_types are gone by now
	"entity_obligations",
	"obligation_types",
	// RBAC / hierarchy projections that point at entities
	"entity_closure",
	"user_grants",
	// identity material that points at users
	"invite_tokens",
	"api_tokens",
	"sessions",
	// data templates: workflow_tasks / task_instances referenced them (SET NULL)
	"data_templates",
	// entities (self-FK is ON DELETE SET NULL, so one bulk delete is fine)
	"entities",
	// users last: every actor column (created_by / updated_by) points here
	"users",
	// the ledger: append-only for the app role, and RESTRICTs the tenants row
	"audit_log",
}

// DeleteTenant removes a tenant and everything belonging to it, identified by
// slug. It is the reset half of the demo dataset tooling: seed, verify, reset,
// seed again — never a production operation.
//
// All deletes run in ONE transaction bound to the tenant (app.tenant_id set,
// ADR-0004), so RLS-scoped tables are reachable and a failure leaves the tenant
// intact rather than half-deleted.
//
// PRIVILEGES: audit_log is deliberately append-only for the application role —
// the migrate job REVOKEs UPDATE/DELETE on it and its RLS policies allow only
// SELECT and INSERT. Deleting a tenant therefore needs a more privileged
// connection (in the compose stack: the postgres superuser, not zentax_app).
// When the connection cannot clear the ledger, this returns an error that says
// exactly that instead of a bare "permission denied".
func DeleteTenant(ctx context.Context, db DB, slug string) error {
	if db == nil {
		return errors.New("seed: nil database")
	}
	if slug == "" {
		return errors.New("seed: tenant slug is required")
	}

	var tenantID string
	err := db.QueryRowxContext(ctx, `SELECT id FROM tenants WHERE slug = $1`, slug).Scan(&tenantID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("delete tenant %q: %w", slug, ErrTenantNotFound)
	case err != nil:
		return fmt.Errorf("delete tenant %q: look up tenant: %w", slug, err)
	}

	if err := deleteTenantByID(ctx, db, tenantID); err != nil {
		return fmt.Errorf("delete tenant %q: %w", slug, err)
	}
	return nil
}

// deleteTenantByID performs the deletion itself, split out so the ordering can
// be tested without a database.
func deleteTenantByID(ctx context.Context, db DB, tenantID string) error {
	return db.WithinTransaction(app.WithTenantID(ctx, tenantID), func(txCtx context.Context) error {
		for _, table := range tenantTables {
			// The table name is from the fixed list above, never from input.
			if _, err := db.ExecContext(txCtx,
				`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
				if table == "audit_log" && isInsufficientPrivilege(err) {
					return fmt.Errorf(
						"clear audit_log: %w (audit_log is append-only for the application role; "+
							"a tenant delete needs a connection allowed to delete from it — "+
							"the superuser DSN, not the app role)", err)
				}
				return fmt.Errorf("clear %s: %w", table, err)
			}
		}
		if _, err := db.ExecContext(txCtx, `DELETE FROM tenants WHERE id = $1`, tenantID); err != nil {
			return fmt.Errorf("delete tenants row: %w", err)
		}
		return nil
	})
}

func isInsufficientPrivilege(err error) bool {
	pgErr := database.IsPgError(err)
	return pgErr != nil && pgErr.Code == pgInsufficientPrivilege
}
