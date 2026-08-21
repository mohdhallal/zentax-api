package usecases

import "github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"

type UseCases struct {
	repo domain.TaskInstanceRepository
}

func NewUseCases(repo domain.TaskInstanceRepository) *UseCases {
	return &UseCases{repo: repo}
}
