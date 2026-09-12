package authz

import (
	"context"
	"errors"
	"testing"
)

// fakeWalker records the roots it was asked to expand and returns a fixed
// subtree, so the memo can be observed by call count.
type fakeWalker struct {
	subtree []string
	roots   [][]string
	err     error
}

func (f *fakeWalker) DescendantsAndSelf(_ context.Context, roots []string) ([]string, error) {
	f.roots = append(f.roots, roots)
	if f.err != nil {
		return nil, f.err
	}
	return f.subtree, nil
}

// A tenant-wide grant carrying the capability reads everything: no predicate,
// and — the point — no database round trip to work that out.
func TestReadScope_TenantWideGrantIsUnboundedAndFree(t *testing.T) {
	walker := &fakeWalker{subtree: []string{"x"}}
	ctx := ctxWith(Grant{Role: RoleViewer}, scoped(RoleManager, "fr"))

	scope, err := ResolveReadScope(ctx, walker, EntityRead)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Unbounded() {
		t.Fatal("a tenant-wide grant with the capability must not be narrowed")
	}
	if len(walker.roots) != 0 {
		t.Fatalf("no walk should have run, got %v", walker.roots)
	}
	if pred := scope.EntityPredicate("e.id", func(any) string { return "$1" }); pred != "" {
		t.Fatalf("unbounded must add no clause, got %q", pred)
	}
}

// Only scoped grants: the readable set is the union of their subtrees, and the
// walk runs ONCE per request even though a list and its count both ask.
func TestReadScope_ScopedGrantsNarrowAndMemoize(t *testing.T) {
	walker := &fakeWalker{subtree: []string{"fr", "fr-sub", "de"}}
	ctx := ctxWith(scoped(RolePreparer, "fr"), scoped(RoleViewer, "de"), scoped(RolePreparer, "fr"))

	for i := range 3 {
		scope, err := ResolveReadScope(ctx, walker, TaskRead)
		if err != nil {
			t.Fatal(err)
		}
		if scope.Unbounded() {
			t.Fatalf("call %d: a scoped principal must be narrowed", i)
		}
		if got := len(scope.EntityIDs()); got != 3 {
			t.Fatalf("call %d: got %d ids, want 3", i, got)
		}
	}
	if len(walker.roots) != 1 {
		t.Fatalf("the subtree walk must be memoized per request, ran %d times", len(walker.roots))
	}
	if got := walker.roots[0]; len(got) != 2 {
		t.Fatalf("duplicate roots must be folded before the walk, got %v", got)
	}
}

// Each capability is resolved on its own (the role matrix could diverge), but
// each is memoized separately rather than re-walked.
func TestReadScope_MemoIsPerCapability(t *testing.T) {
	walker := &fakeWalker{subtree: []string{"fr"}}
	ctx := ctxWith(scoped(RolePreparer, "fr"))
	for _, cap := range []Capability{EntityRead, TaskRead, EntityRead, TaskRead} {
		if _, err := ResolveReadScope(ctx, walker, cap); err != nil {
			t.Fatal(err)
		}
	}
	if len(walker.roots) != 2 {
		t.Fatalf("want one walk per capability, got %d", len(walker.roots))
	}
}

// A context that never passed the capability middleware carries no grants:
// unit tests, the internal router, the seeders and the generator's own reads.
// Those are not request paths and are deliberately NOT narrowed — the same
// posture as a nil *Authorizer on the write side.
func TestReadScope_NoGrantsIsUnbounded(t *testing.T) {
	walker := &fakeWalker{}
	scope, err := ResolveReadScope(context.Background(), walker, EntityRead)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Unbounded() {
		t.Fatal("a context with no grants must not be narrowed")
	}
}

// A principal whose grants carry the capability nowhere reads NOTHING. The
// capability middleware would already have refused such a request, so this is
// a repository asking for a capability its route does not gate: fail closed.
func TestReadScope_GrantsWithoutTheCapabilityReadNothing(t *testing.T) {
	walker := &fakeWalker{subtree: []string{"fr"}}
	// preparer holds task:write but never task:approve.
	ctx := ctxWith(scoped(RolePreparer, "fr"))
	scope, err := ResolveReadScope(ctx, walker, TaskApprove)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Unbounded() || len(scope.EntityIDs()) != 0 {
		t.Fatalf("must be a closed, empty scope, got %+v", scope)
	}
	if len(walker.roots) != 0 {
		t.Fatal("an empty scope needs no walk")
	}
	if pred := scope.EntityPredicate("e.id", func(any) string { return "$1" }); pred != "FALSE" {
		t.Fatalf("want FALSE, got %q", pred)
	}
}

// The predicates bind the id set; they never interpolate it.
func TestReadScope_PredicatesBindTheEntitySet(t *testing.T) {
	scope := NarrowedReadScope("fr", "fr-sub")

	var bound []any
	bind := func(v any) string {
		bound = append(bound, v)
		return "$7"
	}
	if got := scope.EntityPredicate("w.entity_id", bind); got != "w.entity_id = ANY($7::uuid[])" {
		t.Fatalf("entity predicate = %q", got)
	}
	want := "EXISTS (SELECT 1 FROM workflows sw WHERE sw.id = ti.workflow_id" +
		" AND sw.entity_id = ANY($7::uuid[]))"
	if got := scope.WorkflowPredicate("ti.workflow_id", bind); got != want {
		t.Fatalf("workflow predicate = %q\nwant %q", got, want)
	}
	if len(bound) != 2 {
		t.Fatalf("each predicate must bind exactly once, got %v", bound)
	}
	for _, v := range bound {
		ids, ok := v.([]string)
		if !ok || len(ids) != 2 || ids[0] != "fr" {
			t.Fatalf("bound value = %#v", v)
		}
	}
	if scope.WorkflowPredicate("ti.workflow_id", bind) == "" {
		t.Fatal("a narrowed scope must always produce a clause")
	}
	if NarrowedReadScope().WorkflowPredicate("ti.workflow_id", bind) != "FALSE" {
		t.Fatal("an empty scope must fail closed on the workflow predicate too")
	}
	if UnboundedReadScope().WorkflowPredicate("ti.workflow_id", bind) != "" {
		t.Fatal("unbounded must add no clause")
	}
}

// A failing walk is an error, never a silently wide read.
func TestReadScope_WalkErrorDoesNotWiden(t *testing.T) {
	boom := errors.New("boom")
	walker := &fakeWalker{err: boom}
	scope, err := ResolveReadScope(ctxWith(scoped(RoleViewer, "fr")), walker, EntityRead)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if scope.Unbounded() {
		t.Fatal("a failed walk must not return an unbounded scope")
	}
}

func TestAndPredicate(t *testing.T) {
	if got := AndPredicate("", "a = 1", "", "b = 2"); got != "a = 1 AND b = 2" {
		t.Fatalf("got %q", got)
	}
	if got := AndPredicate("", ""); got != "" {
		t.Fatalf("got %q", got)
	}
}
