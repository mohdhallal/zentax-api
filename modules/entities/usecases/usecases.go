package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type UseCases struct {
	repo       domain.EntityRepository
	authorizer *authz.Authorizer
	audit      *audit.Recorder
}

// NewUseCases builds the entity use cases. The authorizer is optional (variadic)
// so unit tests can construct without scope enforcement; the application always
// wires one (see bootstrap.NewContainer). A nil authorizer is a safe no-op.
func NewUseCases(repo domain.EntityRepository, authorizer ...*authz.Authorizer) *UseCases {
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
