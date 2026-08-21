package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type UseCases struct {
	repo       domain.ObligationTypeRepository
	authorizer *authz.Authorizer
}

// NewUseCases builds the obligation-type use cases; the authorizer is optional
// (variadic) so unit tests skip scope checks while the app always wires one.
func NewUseCases(repo domain.ObligationTypeRepository, authorizer ...*authz.Authorizer) *UseCases {
	uc := &UseCases{repo: repo}
	if len(authorizer) > 0 {
		uc.authorizer = authorizer[0]
	}
	return uc
}
