package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type UseCases struct {
	repo       domain.EntityObligationRepository
	authorizer *authz.Authorizer
	audit      *audit.Recorder
}

// NewUseCases builds the entity-obligation use cases; the authorizer is optional
// (variadic) so unit tests skip scope checks while the app always wires one.
func NewUseCases(repo domain.EntityObligationRepository, authorizer ...*authz.Authorizer) *UseCases {
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
