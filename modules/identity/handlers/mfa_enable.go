package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/identity/dto"
)

// MfaEnableHandler confirms an enrolled TOTP secret and turns MFA on.
type MfaEnableHandler struct {
	usecases domain.AuthUseCases
}

func NewMfaEnableHandler(uc domain.AuthUseCases) *MfaEnableHandler {
	return &MfaEnableHandler{usecases: uc}
}

func (h *MfaEnableHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/mfa/enable",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *MfaEnableHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.MfaCodeBody{}}
}

func (h *MfaEnableHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.MfaCodeBody)

	if err := h.usecases.MfaEnable(r.Context(), requester.ID, body.Code); err != nil {
		return nil, err
	}
	return httpkit.NoContent(), nil
}
