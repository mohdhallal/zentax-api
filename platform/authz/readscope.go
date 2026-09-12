package authz

import (
	"context"
	"strings"
	"sync"
)

// Read-side scope (ADR-0012, increment B-3).
//
// The write side asks "may this principal touch THIS resource" and answers per
// target (Authorizer.Ensure*). A read cannot be asked that way: a list has no
// single target, and a route that remembered to ask would still be a route that
// could forget. So the read side is a PREDICATE, resolved from the grants the
// capability middleware already put on the context and applied by the
// repositories themselves — the same layer, and the same "nobody has to
// remember" property, as the tenant predicate RLS applies underneath it
// (ADR-0004).
//
// The rule mirrors authorize() exactly:
//
//   - a tenant-wide grant carrying the capability reads everything in the
//     tenant, including rows with no owning entity (tenant-level project work).
//     Such a principal is Unbounded and pays nothing: no predicate, no walk.
//   - otherwise the readable set is the union of the grants' entity subtrees,
//     and a row whose owning entity is NULL is NOT in it — a tenant-level
//     resource needs a tenant-wide grant, exactly as it does for writes.
//   - a context with no grants at all is Unbounded. That is not a hole: grants
//     are absent only where RequireCapability never ran — unit tests, the
//     internal router, the seeders and the generator's own reads. Every
//     external tenant route declares a Capability, so the request path always
//     carries them (audited: all 40 read routes of the seven read modules).
//
// Tenant-level REFERENCE data is deliberately never narrowed: obligation
// types, data templates and the member directory carry no entity, so a subtree
// predicate would hide them from every scoped user and the product cannot
// render a workflow, a tax-data form or an assignee picker without them.
//
// # Why a recursive walk and not the entity_closure table
//
// entity_closure has existed, empty, since the RBAC schema migration, created
// for exactly this. It stays empty, and the decision is a measurement, not a
// preference. On the largest tenant that exists (the scale fixture: 48
// entities, depth 3, 1 925 workflows, 97 152 task instances), warm cache,
// median of three EXPLAIN ANALYZE runs of the statements this package
// produces, scoping to a 16-entity subtree:
//
//	shape                          task page (plan+exec)  exact count
//	no scope at all (baseline)          1.1 + 0.25 ms     1.0 + 26.7 ms
//	subtree walk inlined per statement  1.9 + 7.28 ms     2.0 + 59.7 ms
//	closure sub-select per statement    1.0 + 6.70 ms     1.0 + 19.7 ms
//	ids resolved once, BOUND as uuid[]  0.9 + 0.30 ms     1.0 + 24.9 ms
//	  the resolve itself: walk 0.11 ms, closure lookup 0.02 ms
//
// Two things fall out. First, the decision that matters is not walk-versus-
// closure but whether the subtree is expanded inside every statement or
// resolved once and bound: binding is 24x faster on the page and takes the
// exact count BELOW the unscoped baseline, because the array restricts the
// workflow side of the join. That is what this package does. Second, once the
// set is resolved once per request, the walk and the closure differ by 0.09 ms
// on a request that costs tens of milliseconds — while the closure would add a
// backfill, maintenance on every entity create, reparent and delete, and an
// invariant whose silent drift WIDENS someone's read scope. That is a security
// bug wearing a performance bug's clothes, bought for 0.09 ms.
//
// The trade flips on tree SIZE, not on tenant data volume: the walk rescans
// the tenant's entities once per level (there is no index on
// parent_entity_id), so a synthetic 5 048-entity forest of depth 6 costs
// 9.5-17 ms against the closure's 1.1 ms. That is ~100x the largest tenant
// today. Populate and use entity_closure when a single tenant approaches
// ~1 000 entities, or when the resolve query shows up in the ADR-0021 budget —
// and add the index on (tenant_id, parent_entity_id) first, since it is the
// cheaper half of the same fix.

// DescendantWalker expands scope roots into the entity ids they cover. It is a
// separate, single-method port rather than a method on ScopeResolver so the
// write path's resolver and its test fake stay untouched.
type DescendantWalker interface {
	// DescendantsAndSelf returns roots plus every descendant, once each. It
	// runs on the request transaction, so RLS confines it to the tenant.
	DescendantsAndSelf(ctx context.Context, roots []string) ([]string, error)
}

// ReadScope is the resolved answer for one capability: either "everything in
// the tenant" or a concrete, closed set of entity ids.
type ReadScope struct {
	unbounded bool
	entityIDs []string
}

// UnboundedReadScope reads the whole tenant — a tenant-wide grant, or a context
// that never passed the capability gate.
func UnboundedReadScope() ReadScope { return ReadScope{unbounded: true} }

// NarrowedReadScope is a scope limited to a known entity set. The application
// obtains its scopes from ResolveReadScope; this exists so the repositories'
// SQL-shape tests can assert the narrowed statement without a database, and so
// an empty set (read nothing) can be expressed explicitly.
func NarrowedReadScope(entityIDs ...string) ReadScope {
	return ReadScope{entityIDs: entityIDs}
}

// Unbounded reports whether the scope narrows nothing.
func (s ReadScope) Unbounded() bool { return s.unbounded }

// EntityIDs is the readable entity set — nil when Unbounded.
func (s ReadScope) EntityIDs() []string { return s.entityIDs }

// Bind adds one parameter to a statement being assembled and returns its
// placeholder. Both hand-written where-builders in the read modules and the
// shared base repository already expose exactly this.
type Bind func(any) string

// EntityPredicate renders the scope as a SQL predicate over an entity-id
// column ("entities.id", "w.entity_id"): the empty string when the scope is
// unbounded (add no clause), `FALSE` when the principal can read nothing, and
// `col = ANY($n)` otherwise. The ids are BOUND, never interpolated, and the
// bound array is what makes the predicate cheap: the planner sees a single
// parameter instead of a correlated subtree walk per statement.
//
// NULL is not in the set by construction: `NULL = ANY(...)` is NULL, so a row
// with no owning entity drops out of a narrowed read — which is the rule.
func (s ReadScope) EntityPredicate(col string, bind Bind) string {
	if s.unbounded {
		return ""
	}
	if len(s.entityIDs) == 0 {
		return "FALSE"
	}
	return col + " = ANY(" + bind(s.entityIDs) + "::uuid[])"
}

// WorkflowPredicate is EntityPredicate one join further out, for tables that
// reach their entity through a workflow (workflow tasks, task instances,
// documents): the workflow id column must name a workflow whose entity is in
// the scope. An EXISTS rather than a join so the caller's row count, ordering
// and aggregates are untouched.
func (s ReadScope) WorkflowPredicate(workflowIDCol string, bind Bind) string {
	if s.unbounded {
		return ""
	}
	if len(s.entityIDs) == 0 {
		return "FALSE"
	}
	return "EXISTS (SELECT 1 FROM workflows sw WHERE sw.id = " + workflowIDCol +
		" AND sw.entity_id = ANY(" + bind(s.entityIDs) + "::uuid[]))"
}

// ReadScopeRoots is the pure half of the decision, taken from the grants alone:
// the scope roots of the grants that carry cap, or unbounded when one of them
// is tenant-wide. It touches no database, which is the common case — every
// admin, manager, reviewer, preparer and viewer holding a tenant-wide grant
// resolves here and pays nothing.
func ReadScopeRoots(grants []Grant, cap Capability) (roots []string, unbounded bool) {
	if len(grants) == 0 {
		// No capability gate ran on this context: not a request path.
		return nil, true
	}
	seen := map[string]bool{}
	for _, g := range grants {
		if !RoleHasCapability(g.Role, cap) {
			continue
		}
		if g.ScopeEntityID == nil {
			return nil, true
		}
		if !seen[*g.ScopeEntityID] {
			seen[*g.ScopeEntityID] = true
			roots = append(roots, *g.ScopeEntityID)
		}
	}
	// No grant carries cap. The capability middleware would already have
	// refused the request, so this is a repository asking for a capability its
	// route does not gate: read nothing rather than everything.
	return roots, false
}

// ResolveReadScope answers the read-scope question for cap on this request,
// expanding the scope roots through walker when it has to. The expansion is
// memoized per request and per capability (the cache is planted by WithGrants),
// so a list and its count share one walk.
func ResolveReadScope(ctx context.Context, walker DescendantWalker, cap Capability) (ReadScope, error) {
	roots, unbounded := ReadScopeRoots(GrantsFrom(ctx), cap)
	if unbounded {
		return UnboundedReadScope(), nil
	}
	if len(roots) == 0 {
		return ReadScope{}, nil
	}

	cache := readScopeCacheFrom(ctx)
	if scope, ok := cache.get(cap); ok {
		return scope, nil
	}

	ids, err := walker.DescendantsAndSelf(ctx, roots)
	if err != nil {
		return ReadScope{}, err
	}
	scope := ReadScope{entityIDs: ids}
	cache.put(cap, scope)
	return scope, nil
}

// readScopeCache memoizes the subtree expansion for the life of one request.
// A nil cache (a context that never went through WithGrants) is a valid
// no-op: every lookup misses and nothing is stored.
type readScopeCache struct {
	mu     sync.Mutex
	scopes map[Capability]ReadScope
}

func (c *readScopeCache) get(cap Capability) (ReadScope, bool) {
	if c == nil {
		return ReadScope{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	scope, ok := c.scopes[cap]
	return scope, ok
}

func (c *readScopeCache) put(cap Capability, scope ReadScope) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scopes == nil {
		c.scopes = map[Capability]ReadScope{}
	}
	c.scopes[cap] = scope
}

func readScopeCacheFrom(ctx context.Context) *readScopeCache {
	cache, _ := ctx.Value(readScopeCacheKey).(*readScopeCache)
	return cache
}

// AndPredicate joins a scope predicate onto an existing WHERE fragment, both
// possibly empty — a small convenience for the hand-written statements.
func AndPredicate(preds ...string) string {
	kept := make([]string, 0, len(preds))
	for _, p := range preds {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, " AND ")
}
