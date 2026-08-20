package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations/dto"
)

type CreateEntityObligationHandler struct {
	usecases domain.EntityObligationUseCases
}

func NewCreateEntityObligationHandler(uc domain.EntityObligationUseCases) *CreateEntityObligationHandler {
	return &CreateEntityObligationHandler{usecases: uc}
}

func (h *CreateEntityObligationHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *CreateEntityObligationHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body: dto.CreateEntityObligationBody{},
	}
}

func (h *CreateEntityObligationHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.CreateEntityObligationBody)

	eo, err := h.usecases.Create(r.Context(), domain.CreateEntityObligationInput{
		EntityID:         body.EntityID,
		ObligationTypeID: body.ObligationTypeID,
		Jurisdiction:     body.Jurisdiction,
		Periodicity:      body.Periodicity,
		DeadlineRule:     body.DeadlineRule,
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Created(dto.EntityObligationToJSON(eo)), nil
}
