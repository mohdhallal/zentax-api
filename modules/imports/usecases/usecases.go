// Package usecases is the committing half of spreadsheet import: it resolves
// what a file means against the tenant, says exactly what a commit would do,
// and does it.
//
// Three rules govern everything here, and each one is enforced in more than one
// place on purpose.
//
// # The dry run is a promise, not an estimate
//
// Validate resolves every row against the tenant and stores the result: the
// action, the record it resolved to, and the VERSION of that record. Commit
// re-resolves all of it inside its own transaction and compares. If any row now
// resolves differently — a name that was free is taken, a record that was there
// is gone, a record has been edited since — the whole batch is refused and
// nothing is written. So "twelve created and three updated" is what happens, or
// the customer is told why it cannot be.
//
// # All or nothing
//
// A commit runs inside the request's single transaction (the route declares
// Tenant, which implies Tx), so the first refusal of any kind rolls back
// everything, including the audit entries. And a file carrying one invalid row
// never reaches a commit at all: it is stored 'rejected', which the database
// will not let it leave.
//
// # An imported record is governed exactly like a typed one
//
// Every write goes through the same Authorizer call, against the same
// capability, as the equivalent single-record use case, and writes the same
// audit action with the same whitelist. A scoped principal therefore cannot
// import their way outside their subtree, and an auditor reading the trail
// cannot tell — and does not need to care — whether a change arrived by file or
// by hand.
package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// UseCases is the module's business logic.
type UseCases struct {
	repo       domain.ImportRepository
	reader     domain.FileReader
	authorizer *authz.Authorizer
	audit      *audit.Recorder
}

var _ domain.ImportUseCases = (*UseCases)(nil)

// NewUseCases builds the import use cases. The authorizer is optional
// (variadic) so unit tests can construct without scope enforcement, matching
// every other module here; the application always wires one. A nil authorizer
// is a safe no-op — and note that in THIS module a nil authorizer is only safe
// in a test, because the import's authorization is not duplicated anywhere
// else: the routes' declared capability is the tenant-wide gate, and every
// per-entity decision is the authorizer's.
func NewUseCases(repo domain.ImportRepository, reader domain.FileReader, authorizer ...*authz.Authorizer) *UseCases {
	uc := &UseCases{repo: repo, reader: reader}
	if len(authorizer) > 0 {
		uc.authorizer = authorizer[0]
	}
	return uc
}

// WithAudit injects the audit recorder (ADR-0008). Optional and nil-safe, as
// everywhere else; the container always chains it on.
func (uc *UseCases) WithAudit(r *audit.Recorder) *UseCases {
	uc.audit = r
	return uc
}

// Template is the column set a customer should send. It needs neither the
// database nor the tenant — it is the contract, not data — but it lives behind
// the same authenticated route as the rest so that the shape of the product's
// data model is not a public document.
func (uc *UseCases) Template(_ context.Context, kind domain.Kind) []domain.Field {
	return domain.Fields(kind.Target())
}
