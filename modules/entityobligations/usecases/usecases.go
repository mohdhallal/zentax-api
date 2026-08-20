package usecases

import "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"

type UseCases struct {
	repo domain.EntityObligationRepository
}

func NewUseCases(repo domain.EntityObligationRepository) *UseCases {
	return &UseCases{repo: repo}
}
