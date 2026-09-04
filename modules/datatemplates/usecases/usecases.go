package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// UseCases implements domain.DataTemplateUseCases. Data templates are
// tenant-level setup content: every write requires a tenant-wide
// data_template:write grant (no entity subtree applies).
type UseCases struct {
	repo       domain.DataTemplateRepository
	authorizer *authz.Authorizer
	audit      *audit.Recorder
}

var _ domain.DataTemplateUseCases = (*UseCases)(nil)

// NewUseCases builds the data-template use cases; the authorizer is optional
// (variadic) so unit tests and cmd/seed-admin skip scope checks while the app
// always wires one.
func NewUseCases(repo domain.DataTemplateRepository, authorizer ...*authz.Authorizer) *UseCases {
	uc := &UseCases{repo: repo}
	if len(authorizer) > 0 {
		uc.authorizer = authorizer[0]
	}
	return uc
}

// WithAudit injects the audit recorder (ADR-0008). Optional and nil-safe —
// unit tests construct without it; the container always chains it on.
func (uc *UseCases) WithAudit(r *audit.Recorder) *UseCases {
	uc.audit = r
	return uc
}
