package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/identity/dto"
)

// MeHandler returns the authenticated user (full session).
type MeHandler struct {
	usecases domain.AuthUseCases
}

func NewMeHandler(uc domain.AuthUseCases) *MeHandler {
	return &MeHandler{usecases: uc}
}

func (h *MeHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodGet,
		Path:     "/me",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *MeHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	user, err := h.usecases.Me(r.Context(), requester.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.UserToJSON(user)), nil
}
