package authz

import (
	"context"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
)

// ScopeResolver resolves the entity an RBAC-scoped resource belongs to, plus an
// entity's ancestor chain — the inputs to an entity-subtree scope check (ADR-0012,
// Increment B-2). Every lookup is RLS-scoped to the current tenant (it runs on
// the request transaction). Owner lookups return "" when the resource has no
// owning entity (a tenant-level / project resource) or does not exist.
type ScopeResolver interface {
	AncestorsAndSelf(ctx context.Context, entityID string) ([]string, error)
	EntityObligationOwner(ctx context.Context, id string) (string, error)
	WorkflowOwner(ctx context.Context, id string) (string, error)
	WorkflowTaskOwner(ctx context.Context, id string) (string, error)
	TaskInstanceOwner(ctx context.Context, id string) (string, error)
}

// Authorizer enforces per-entity scope on top of the tenant-wide capability gate
// already applied by RequireCapability. It reads the grants stashed on the
// context by that middleware.
//
// A nil *Authorizer is a valid no-op (every method allows): use cases construct
// with an optional authorizer, so unit tests that pass none skip scope checks
// while the wired-up application always enforces them.
type Authorizer struct {
	resolver ScopeResolver
}

func NewAuthorizer(resolver ScopeResolver) *Authorizer {
	return &Authorizer{resolver: resolver}
}

// EnsureEntity authorizes cap against a target entity. entityID == "" means a
// tenant-level resource (no owning entity), which only a tenant-wide grant can
// touch. Use it when the owning entity is known directly (an entity by its own
// id, or a create carrying entity_id).
func (a *Authorizer) EnsureEntity(ctx context.Context, entityID string, cap Capability) error {
	if a == nil {
		return nil
	}
	return a.authorize(ctx, entityID, cap)
}

// EnsureEntityRef is EnsureEntity for a nullable reference (nil → tenant-level).
func (a *Authorizer) EnsureEntityRef(ctx context.Context, entityID *string, cap Capability) error {
	if a == nil {
		return nil
	}
	id := ""
	if entityID != nil {
		id = *entityID
	}
	return a.authorize(ctx, id, cap)
}

// EnsureEntityObligation / EnsureWorkflow / EnsureWorkflowTask / EnsureTaskInstance
// authorize cap against a resource whose owning entity is resolved by id (used by
// update / delete / start, where only the resource id is in hand).
func (a *Authorizer) EnsureEntityObligation(ctx context.Context, id string, cap Capability) error {
	return a.authorizeVia(ctx, ScopeResolver.EntityObligationOwner, id, cap)
}
func (a *Authorizer) EnsureWorkflow(ctx context.Context, id string, cap Capability) error {
	return a.authorizeVia(ctx, ScopeResolver.WorkflowOwner, id, cap)
}
func (a *Authorizer) EnsureWorkflowTask(ctx context.Context, id string, cap Capability) error {
	return a.authorizeVia(ctx, ScopeResolver.WorkflowTaskOwner, id, cap)
}
func (a *Authorizer) EnsureTaskInstance(ctx context.Context, id string, cap Capability) error {
	return a.authorizeVia(ctx, ScopeResolver.TaskInstanceOwner, id, cap)
}

func (a *Authorizer) authorizeVia(
	ctx context.Context,
	ownerFn func(ScopeResolver, context.Context, string) (string, error),
	id string, cap Capability,
) error {
	if a == nil {
		return nil
	}
	owner, err := ownerFn(a.resolver, ctx, id)
	if err != nil {
		return err
	}
	return a.authorize(ctx, owner, cap)
}

// authorize is the core rule. A tenant-wide grant carrying cap authorizes any
// target (including tenant-level, entityID == "") — the common case, resolved
// without touching the database. Otherwise a scoped grant carrying cap
// authorizes only a target entity within its subtree (its scope_entity_id equals
// the target or one of its ancestors). Anything else is 403.
func (a *Authorizer) authorize(ctx context.Context, entityID string, cap Capability) error {
	// Defense in depth for the human-only rule (the capability middleware is
	// the primary gate): a service principal never approves.
	if req := app.GetRequester(ctx); req != nil && req.ServiceAccount && HumanOnly(cap) {
		return apperrors.NewForbidden("this action requires a human user")
	}

	grants := GrantsFrom(ctx)

	var scopes []string // scope ids of scoped grants that carry cap
	for _, g := range grants {
		if !RoleHasCapability(g.Role, cap) {
			continue
		}
		if g.ScopeEntityID == nil {
			return nil // tenant-wide grant with cap → authorized anywhere
		}
		scopes = append(scopes, *g.ScopeEntityID)
	}

	// No tenant-wide grant with cap. A tenant-level resource (no owning entity)
	// cannot be reached by a scoped grant.
	if entityID == "" || len(scopes) == 0 {
		return forbidden()
	}

	chain, err := a.resolver.AncestorsAndSelf(ctx, entityID)
	if err != nil {
		return err
	}
	inChain := make(map[string]bool, len(chain))
	for _, id := range chain {
		inChain[id] = true
	}
	for _, s := range scopes {
		if inChain[s] {
			return nil
		}
	}
	return forbidden()
}

func forbidden() error {
	return apperrors.NewForbidden("insufficient permissions for this entity")
}
