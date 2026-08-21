package authz

import (
	"context"
	"testing"
)

// fakeResolver returns a preset ancestor chain and counts AncestorsAndSelf calls
// (to prove the tenant-wide fast path never hits the resolver).
type fakeResolver struct {
	chains       map[string][]string // entityID → ancestors-and-self
	workflowOwn  map[string]string
	ancestorCall int
}

func (f *fakeResolver) AncestorsAndSelf(_ context.Context, entityID string) ([]string, error) {
	f.ancestorCall++
	if c, ok := f.chains[entityID]; ok {
		return c, nil
	}
	return []string{entityID}, nil
}
func (f *fakeResolver) EntityObligationOwner(_ context.Context, id string) (string, error) {
	return "", nil
}
func (f *fakeResolver) WorkflowOwner(_ context.Context, id string) (string, error) {
	return f.workflowOwn[id], nil
}
func (f *fakeResolver) WorkflowTaskOwner(_ context.Context, id string) (string, error) {
	return "", nil
}
func (f *fakeResolver) TaskInstanceOwner(_ context.Context, id string) (string, error) {
	return "", nil
}

func ctxWith(grants ...Grant) context.Context {
	return WithGrants(context.Background(), grants)
}

func scoped(role Role, entityID string) Grant { return Grant{Role: role, ScopeEntityID: &entityID} }

func TestAuthorize_TenantWideAllowsAnywhereWithoutResolver(t *testing.T) {
	f := &fakeResolver{}
	a := NewAuthorizer(f)
	ctx := ctxWith(Grant{Role: RoleManager}) // tenant-wide

	if err := a.EnsureEntity(ctx, "any-entity", WorkflowWrite); err != nil {
		t.Fatalf("tenant-wide manager should be allowed: %v", err)
	}
	if err := a.EnsureEntity(ctx, "", ObligationTypeWrite); err != nil {
		t.Fatalf("tenant-wide manager should reach tenant-level resources: %v", err)
	}
	if f.ancestorCall != 0 {
		t.Errorf("tenant-wide path must not query ancestors, got %d calls", f.ancestorCall)
	}
}

func TestAuthorize_ScopedGrantCoversSubtreeOnly(t *testing.T) {
	// chain(C) = C → B → A, so a grant scoped to A covers C. chain(D) = D → E.
	f := &fakeResolver{chains: map[string][]string{
		"C": {"C", "B", "A"},
		"D": {"D", "E"},
		"A": {"A"},
	}}
	a := NewAuthorizer(f)
	ctx := ctxWith(scoped(RoleManager, "A"))

	if err := a.EnsureEntity(ctx, "A", WorkflowWrite); err != nil {
		t.Errorf("scope A should cover A: %v", err)
	}
	if err := a.EnsureEntity(ctx, "C", WorkflowWrite); err != nil {
		t.Errorf("scope A should cover descendant C: %v", err)
	}
	if err := a.EnsureEntity(ctx, "D", WorkflowWrite); err == nil {
		t.Error("scope A must NOT cover unrelated D")
	}
	// A scoped grant can never reach a tenant-level resource.
	if err := a.EnsureEntity(ctx, "", ObligationTypeWrite); err == nil {
		t.Error("scoped grant must not reach a tenant-level resource")
	}
}

func TestAuthorize_ResolvesOwningEntityForIndirectResource(t *testing.T) {
	// workflow W belongs to entity C (under A); a grant scoped to A covers it.
	f := &fakeResolver{
		chains:      map[string][]string{"C": {"C", "B", "A"}},
		workflowOwn: map[string]string{"W": "C"},
	}
	a := NewAuthorizer(f)
	ctx := ctxWith(scoped(RoleManager, "A"))
	if err := a.EnsureWorkflow(ctx, "W", WorkflowWrite); err != nil {
		t.Errorf("scope A should cover workflow W (owned by C, under A): %v", err)
	}
	// A workflow owned by an out-of-scope entity is denied.
	f.workflowOwn["W2"] = "D"
	f.chains["D"] = []string{"D"}
	if err := a.EnsureWorkflow(ctx, "W2", WorkflowWrite); err == nil {
		t.Error("scope A must not cover workflow W2 (owned by unrelated D)")
	}
}

func TestAuthorize_NoCapabilityIsDenied(t *testing.T) {
	a := NewAuthorizer(&fakeResolver{})
	ctx := ctxWith(Grant{Role: RoleViewer}) // viewer lacks workflow:write
	if err := a.EnsureEntity(ctx, "A", WorkflowWrite); err == nil {
		t.Error("viewer must be denied workflow:write")
	}
}

func TestAuthorize_NilAuthorizerIsNoOp(t *testing.T) {
	var a *Authorizer
	if err := a.EnsureEntity(context.Background(), "A", WorkflowWrite); err != nil {
		t.Errorf("nil authorizer must allow (no-op): %v", err)
	}
	if err := a.EnsureWorkflow(context.Background(), "W", WorkflowWrite); err != nil {
		t.Errorf("nil authorizer must allow (no-op): %v", err)
	}
}
