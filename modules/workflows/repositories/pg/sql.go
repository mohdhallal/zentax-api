package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/platform/authz"
	authzpg "github.com/mohamadhallal/zentax-api/platform/authz/pg"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

// workflowColumns is the domain projection — WITHOUT tenant_id (RLS infra).
// selected_periods (JSONB) and due_date_rule (JSONB) scan into the domain
// Periods / DueDateRule Scanner/Valuer types. RETURNING clauses use this bare
// form; reads use workflowReadColumns (alias-qualified + the joined names).
const workflowColumns = `id, name, description, workflow_category, project_type, financial_year, ` +
	`periodicity, selected_periods, entity_id, obligation_type_id, due_date_rule, start_date, end_date, ` +
	`tasks_sequential, status, created_at, updated_at, created_by, updated_by`

// workflowReadColumns is the read projection: every workflow column qualified
// by the `w` alias plus the referenced entity's and obligation type's names.
const workflowReadColumns = `w.id, w.name, w.description, w.workflow_category, w.project_type, w.financial_year, ` +
	`w.periodicity, w.selected_periods, w.entity_id, w.obligation_type_id, w.due_date_rule, w.start_date, w.end_date, ` +
	`w.tasks_sequential, w.status, w.created_at, w.updated_at, w.created_by, w.updated_by, ` +
	`e.name AS entity_name, ot.name AS obligation_type_name`

// workflowReadFrom LEFT JOINs the two referenced tables so list rows carry the
// names without an N+1 lookup. entities and obligation_types are RLS-scoped
// like workflows, so a name can only ever come from the same tenant. Every
// column the base repository renders (filters, sort, tie-breaker, search) is
// `w.`-qualified so `status` / `name` are unambiguous across the join.
const workflowReadFrom = ` FROM workflows w` +
	` LEFT JOIN entities e ON e.id = w.entity_id` +
	` LEFT JOIN obligation_types ot ON ot.id = w.obligation_type_id`

var sqlConfig = baserepo.SQLConfig{
	AllowedColumns: map[string]bool{
		"w.created_at":         true,
		"w.name":               true,
		"w.workflow_category":  true,
		"w.status":             true,
		"w.entity_id":          true,
		"w.obligation_type_id": true,
		"w.financial_year":     true,
	},
	DefaultOrderBy:   "w.created_at",
	DefaultOrderDesc: true, // newest first
	TieBreaker:       "w.id",
	// financial_year is NULL on project workflows: `financialYear=none` keeps
	// them inside a fiscal-year scope (baserepo.NullFilterValue).
	NullableFilters: map[string]bool{"w.financial_year": true},
	// `search` matches the workflow name only (ILIKE, escaped).
	SearchColumns: []string{"w.name"},
	GetById:       `SELECT ` + workflowReadColumns + workflowReadFrom + ` WHERE w.id = $1 LIMIT 1`,
	// tenant_id defaults from the GUC; selected_periods/due_date_rule/status default in DDL.
	Create: `
		INSERT INTO workflows (
			name, description, workflow_category, project_type, financial_year, periodicity,
			selected_periods, entity_id, obligation_type_id, due_date_rule, start_date, end_date, tasks_sequential
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING ` + workflowColumns,
	Update: `
		UPDATE workflows
		SET name = $2,
		    description = $3,
		    workflow_category = $4,
		    project_type = $5,
		    financial_year = $6,
		    periodicity = $7,
		    selected_periods = $8,
		    entity_id = $9,
		    obligation_type_id = $10,
		    due_date_rule = $11,
		    start_date = $12,
		    end_date = $13,
		    tasks_sequential = $14,
		    status = $15,
		    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + workflowColumns,
	Delete: `DELETE FROM workflows WHERE id = $1`,
	// The count stays over workflows alone under the same alias, so the WHERE
	// the base repository appends to both statements binds identically.
	Count:    `SELECT COUNT(*)::int AS total FROM workflows w`,
	ListBase: `SELECT ` + workflowReadColumns + workflowReadFrom,
}

// readScope narrows every read of this repository to the caller's entity
// subtree (ADR-0012 B-3). The anchor is the workflow's entity_id, left
// UNQUALIFIED on purpose: it is the one column name that is correct in all
// three statements the base repository builds — the list and the count over
// `workflows w` (entity_id exists on no other table in the join: not on
// entities, not on obligation_types) and the GetById wrapper, where it is the
// projected column. Were a joined table ever to grow an entity_id, Postgres
// would refuse the statement as ambiguous — loudly, not silently wide.
//
// A workflow with NO entity — tenant-level project work — matches nothing here
// (`NULL = ANY(...)` is NULL), so it stays visible only to a tenant-wide grant,
// exactly as the write side already treats it.
func readScope(db database.ExecerPg) func(context.Context, func(any) string) (string, error) {
	return func(ctx context.Context, bind func(any) string) (string, error) {
		scope, err := authzpg.ReadScope(ctx, db, authz.WorkflowRead)
		if err != nil {
			return "", err
		}
		return scope.EntityPredicate("entity_id", bind), nil
	}
}
