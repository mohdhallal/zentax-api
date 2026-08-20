package usecases

import "github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"

type UseCases struct {
	repo domain.ObligationTypeRepository
}

func NewUseCases(repo domain.ObligationTypeRepository) *UseCases {
	return &UseCases{repo: repo}
}
