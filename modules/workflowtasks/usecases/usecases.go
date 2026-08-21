package usecases

import "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"

type UseCases struct {
	repo domain.WorkflowTaskRepository
}

func NewUseCases(repo domain.WorkflowTaskRepository) *UseCases {
	return &UseCases{repo: repo}
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
