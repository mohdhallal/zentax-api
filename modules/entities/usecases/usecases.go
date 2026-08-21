package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/entities/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type UseCases struct {
	repo       domain.EntityRepository
	authorizer *authz.Authorizer
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
