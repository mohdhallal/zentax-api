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

const grantsKey ctxKey = iota

// WithGrants stashes the requester's grants on the context after they have been
// loaded (once) by the capability middleware, so resource handlers can reuse
// them for per-entity scope checks without re-querying.
func WithGrants(ctx context.Context, grants []Grant) context.Context {
	return context.WithValue(ctx, grantsKey, grants)
}

// GrantsFrom returns the grants stashed by the capability middleware, or nil.
func GrantsFrom(ctx context.Context) []Grant {
	grants, _ := ctx.Value(grantsKey).([]Grant)
	return grants
}
