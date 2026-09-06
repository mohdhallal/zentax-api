package seed

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteTenant_UnknownSlug(t *testing.T) {
	t.Parallel()
	db := &fakeDB{} // every QueryRowx scans sql.ErrNoRows

	err := DeleteTenant(t.Context(), db, "ghost")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTenantNotFound)
	assert.Contains(t, err.Error(), `"ghost"`)
	assert.Empty(t, db.execs, "nothing is deleted when the tenant does not exist")
	require.Len(t, db.queries, 1)
	assert.Contains(t, db.queries[0].Query, "SELECT id FROM tenants WHERE slug = $1")
	assert.Equal(t, []any{"ghost"}, db.queries[0].Args)
}

func TestDeleteTenant_RejectsEmptyInput(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, DeleteTenant(t.Context(), &fakeDB{}, ""), "tenant slug is required")
	require.ErrorContains(t, DeleteTenant(t.Context(), nil, "acme"), "nil database")
}

func TestDeleteTenantByID_DeletesEveryTenantTableThenTheTenantRow(t *testing.T) {
	t.Parallel()
	db := &fakeDB{}

	require.NoError(t, deleteTenantByID(t.Context(), db, "tenant-1"))

	statements := db.execQueries()
	require.Len(t, statements, len(tenantTables)+1)

	for i, table := range tenantTables {
		assert.Equal(t, "DELETE FROM "+table+" WHERE tenant_id = $1", statements[i])
		assert.Equal(t, []any{"tenant-1"}, db.execs[i].Args)
	}
	last := len(statements) - 1
	assert.Equal(t, "DELETE FROM tenants WHERE id = $1", statements[last])
	assert.Equal(t, []any{"tenant-1"}, db.execs[last].Args)
}

func TestDeleteTenantByID_RunsInOneTenantBoundTransaction(t *testing.T) {
	t.Parallel()
	db := &fakeDB{}

	require.NoError(t, deleteTenantByID(t.Context(), db, "tenant-1"))

	// One transaction, bound to the tenant so RLS-scoped tables are reachable
	// (ADR-0004) and a failure rolls the whole reset back.
	assert.Equal(t, []string{"tenant-1"}, db.txTenants)
}

// TestTenantTables_RespectsForeignKeyOrder encodes the schema's referential
// dependencies: a table must be cleared before every table it points at.
// A migration that adds a new tenant-scoped table will fail here until it is
// placed correctly in tenantTables.
func TestTenantTables_RespectsForeignKeyOrder(t *testing.T) {
	t.Parallel()

	// child -> tables the child references (which must be deleted later).
	references := map[string][]string{
		"document_versions":  {"documents"},
		"documents":          {"workflows", "task_instances", "users"},
		"task_instances":     {"workflows", "workflow_tasks", "data_templates", "users"},
		"workflow_tasks":     {"workflows", "data_templates", "users"},
		"workflows":          {"entities", "obligation_types", "users"},
		"entity_obligations": {"entities", "obligation_types", "users"},
		"obligation_types":   {"users"},
		"entity_closure":     {"entities"},
		"user_grants":        {"entities", "users"},
		"invite_tokens":      {"users"},
		"api_tokens":         {"users"},
		"sessions":           {"users"},
		"data_templates":     {"users"},
		"entities":           {"users"},
	}

	position := map[string]int{}
	for i, table := range tenantTables {
		_, duplicate := position[table]
		require.False(t, duplicate, "%s appears twice in tenantTables", table)
		position[table] = i
	}

	for child, parents := range references {
		childPos, ok := position[child]
		require.True(t, ok, "%s is missing from tenantTables", child)
		for _, parent := range parents {
			parentPos, ok := position[parent]
			require.True(t, ok, "%s is missing from tenantTables", parent)
			assert.Less(t, childPos, parentPos,
				"%s references %s, so it must be deleted first", child, parent)
		}
	}

	// audit_log RESTRICTs the tenants row, so it has to be gone before the
	// tenant itself — being last in the list guarantees that.
	assert.Equal(t, "audit_log", tenantTables[len(tenantTables)-1])
	assert.False(t, slices.Contains(tenantTables, "tenants"),
		"the tenants row is deleted separately, after the list")
}

func TestDeleteTenantByID_AuditLogPermissionErrorExplainsItself(t *testing.T) {
	t.Parallel()
	db := &fakeDB{
		execErrMatch: "DELETE FROM audit_log",
		execErr:      &pgconn.PgError{Code: pgInsufficientPrivilege, Message: "permission denied for table audit_log"},
	}

	err := deleteTenantByID(t.Context(), db, "tenant-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "append-only")
	assert.Contains(t, err.Error(), "superuser DSN")
	assert.Contains(t, err.Error(), "permission denied for table audit_log")
	assert.NotContains(t, db.execQueries(), "DELETE FROM tenants WHERE id = $1",
		"the tenant row must survive when the ledger cannot be cleared")
}

func TestDeleteTenantByID_OtherErrorsNameTheTable(t *testing.T) {
	t.Parallel()
	db := &fakeDB{
		execErrMatch: "DELETE FROM workflows",
		execErr:      errors.New("connection reset"),
	}

	err := deleteTenantByID(t.Context(), db, "tenant-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "clear workflows")
	assert.Contains(t, err.Error(), "connection reset")
	assert.NotContains(t, err.Error(), "append-only", "only audit_log gets the privilege hint")
}

func TestDeleteTenant_WrapsTheSlugIntoTheError(t *testing.T) {
	t.Parallel()
	db := &fakeDB{}
	err := DeleteTenant(t.Context(), db, "acme")
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), `delete tenant "acme":`), "got %q", err.Error())
}

func TestIsInsufficientPrivilege(t *testing.T) {
	t.Parallel()
	assert.True(t, isInsufficientPrivilege(&pgconn.PgError{Code: "42501"}))
	assert.False(t, isInsufficientPrivilege(&pgconn.PgError{Code: "23503"}))
	assert.False(t, isInsufficientPrivilege(errors.New("not a pg error")))
	assert.False(t, isInsufficientPrivilege(nil))
}
