package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/identity/dto"
)

// MfaEnrollHandler starts TOTP enrollment for the logged-in user (full session).
type MfaEnrollHandler struct {
	usecases domain.AuthUseCases
}

func NewMfaEnrollHandler(uc domain.AuthUseCases) *MfaEnrollHandler {
	return &MfaEnrollHandler{usecases: uc}
}

func (h *MfaEnrollHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/mfa/enroll",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *MfaEnrollHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	res, err := h.usecases.MfaEnroll(r.Context(), requester.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.MfaEnrollToJSON(res)), nil
}
