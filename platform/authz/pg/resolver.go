// Package authzpg is the Postgres implementation of authz.ScopeResolver — the
// entity-subtree lookups behind scoped RBAC (ADR-0012, Increment B-2). Every
// query runs on the request transaction, so RLS confines it to the tenant.
package authzpg

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var (
	_ authz.ScopeResolver    = (*Resolver)(nil)
	_ authz.DescendantWalker = (*Resolver)(nil)
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

// DescendantsAndSelf returns the roots plus every entity beneath them, by
// walking parent_entity_id downward — the read side's question (ADR-0012 B-3),
// where the write side asks for ancestors. Duplicates are impossible in a tree;
// overlapping roots (a grant on a parent and on its child) are folded by the
// UNION. An empty roots slice returns no ids, never the whole tenant.
//
// This is the same recursive walk the write path uses, deliberately NOT the
// entity_closure table: measured on the largest tenant (48 entities, depth 3)
// the walk costs 0.11 ms against the closure's 0.02 ms, once per request, while
// the closure would add a backfill, maintenance on every create / reparent /
// delete, and an invariant whose silent drift WIDENS a read scope. The full
// measurement, and the tree size at which that trade flips, are recorded in
// platform/authz/readscope.go.
func (r *Resolver) DescendantsAndSelf(ctx context.Context, roots []string) ([]string, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	const q = `
WITH RECURSIVE subtree AS (
    SELECT id FROM entities WHERE id = ANY($1::uuid[])
    UNION
    SELECT e.id
    FROM entities e
    JOIN subtree s ON e.parent_entity_id = s.id
)
SELECT id FROM subtree`
	var ids []string
	if err := r.db.SelectContext(ctx, &ids, q, roots); err != nil {
		return nil, err
	}
	return ids, nil
}

// ReadScope resolves the requester's readable entity set for cap on the given
// connection — the one call a repository makes before assembling a read
// statement. It is a package function so a repository needs nothing injected:
// the grants are on the context and the tree is in the database it already
// holds.
func ReadScope(ctx context.Context, db database.ExecerPg, cap authz.Capability) (authz.ReadScope, error) {
	return authz.ResolveReadScope(ctx, NewResolver(db), cap)
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
