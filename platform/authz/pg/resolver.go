// Package authzpg is the Postgres implementation of authz.ScopeResolver — the
// entity-subtree lookups behind scoped RBAC (ADR-0012, Increment B-2). Every
// query runs on the request transaction, so RLS confines it to the tenant.
package authzpg

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mohamadhallal/zentax-api/platform/database"
)

type Resolver struct {
	db database.ExecerPg
}

func NewResolver(db database.ExecerPg) *Resolver {
	return &Resolver{db: db}
}

// AncestorsAndSelf returns entityID plus every ancestor id, by walking
// parent_entity_id upward. Returns an empty slice if the entity is not visible.
func (r *Resolver) AncestorsAndSelf(ctx context.Context, entityID string) ([]string, error) {
	const q = `
WITH RECURSIVE chain AS (
    SELECT id, parent_entity_id FROM entities WHERE id = $1
    UNION ALL
    SELECT e.id, e.parent_entity_id
    FROM entities e
    JOIN chain c ON e.id = c.parent_entity_id
)
SELECT id FROM chain`
	var ids []string
	if err := r.db.SelectContext(ctx, &ids, q, entityID); err != nil {
		return nil, err
	}
	return ids, nil
}

func (r *Resolver) EntityObligationOwner(ctx context.Context, id string) (string, error) {
	return r.owner(ctx, `SELECT entity_id FROM entity_obligations WHERE id = $1`, id)
}

func (r *Resolver) WorkflowOwner(ctx context.Context, id string) (string, error) {
	return r.owner(ctx, `SELECT entity_id FROM workflows WHERE id = $1`, id)
}

func (r *Resolver) WorkflowTaskOwner(ctx context.Context, id string) (string, error) {
	return r.owner(ctx,
		`SELECT w.entity_id FROM workflow_tasks wt JOIN workflows w ON w.id = wt.workflow_id WHERE wt.id = $1`, id)
}

func (r *Resolver) TaskInstanceOwner(ctx context.Context, id string) (string, error) {
	return r.owner(ctx,
		`SELECT w.entity_id FROM task_instances ti JOIN workflows w ON w.id = ti.workflow_id WHERE ti.id = $1`, id)
}

// owner runs a single-column lookup, mapping "no row" and SQL NULL alike to ""
// (no owning entity / tenant-level / not found) — the authorizer treats "" as a
// tenant-level target that only a tenant-wide grant can reach.
func (r *Resolver) owner(ctx context.Context, query, id string) (string, error) {
	var owner sql.NullString
	err := r.db.GetContext(ctx, &owner, query, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !owner.Valid {
		return "", nil
	}
	return owner.String, nil
}
