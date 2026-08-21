package usecases

import "github.com/mohamadhallal/zentax-api/modules/workflows/domain"

type UseCases struct {
	repo domain.WorkflowRepository
}

func NewUseCases(repo domain.WorkflowRepository) *UseCases {
	return &UseCases{repo: repo}
}
