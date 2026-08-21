package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type UseCases struct {
	repo       domain.WorkflowTaskRepository
	authorizer *authz.Authorizer
}

// NewUseCases builds the workflow-task use cases; the authorizer is optional
// (variadic) so unit tests skip scope checks while the app always wires one.
func NewUseCases(repo domain.WorkflowTaskRepository, authorizer ...*authz.Authorizer) *UseCases {
	uc := &UseCases{repo: repo}
	if len(authorizer) > 0 {
		uc.authorizer = authorizer[0]
	}
	return uc
}

// applyDueDateDefaults fills the offset-rule fields the DB defaults would set,
// since the INSERT/UPDATE always passes explicit values.
func applyDueDateDefaults(reference, unit, direction *string) {
	if *reference == "" {
		*reference = "filing_deadline"
	}
	if *unit == "" {
		*unit = "days"
	}
	if *direction == "" {
		*direction = "before"
	}
}
