package usecases

import "github.com/mohamadhallal/zentax-api/modules/entities/domain"

type UseCases struct {
	repo domain.EntityRepository
}

func NewUseCases(repo domain.EntityRepository) *UseCases {
	return &UseCases{repo: repo}
}
