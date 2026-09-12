package authz

import "context"

// GrantLoader loads a user's grants within the current tenant transaction. It is
// satisfied by the identity module's GrantRepo and consumed by the capability
// middleware; declared here so neither the router nor the middleware needs to
// import the identity module.
type GrantLoader interface {
	ListForUser(ctx context.Context, userID string) ([]Grant, error)
}

type ctxKey int

const (
	grantsKey ctxKey = iota
	readScopeCacheKey
)

// WithGrants stashes the requester's grants on the context after they have been
// loaded (once) by the capability middleware, so resource handlers can reuse
// them for per-entity scope checks without re-querying.
//
// It plants the read-scope memo alongside them (ADR-0012 B-3): the repositories
// resolve a scoped principal's entity subtree lazily, and the memo makes that
// one walk per request rather than one per statement — a list and its count
// share it. Lifetime is the request, because the context is.
func WithGrants(ctx context.Context, grants []Grant) context.Context {
	ctx = context.WithValue(ctx, grantsKey, grants)
	return context.WithValue(ctx, readScopeCacheKey, &readScopeCache{})
}

// GrantsFrom returns the grants stashed by the capability middleware, or nil.
func GrantsFrom(ctx context.Context) []Grant {
	grants, _ := ctx.Value(grantsKey).([]Grant)
	return grants
}
