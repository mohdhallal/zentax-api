package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/identity/dto"
)

// MfaVerifyHandler completes an mfa_pending session. Public (reads the pending
// session cookie itself); the use case validates the code + pending state and
// rotates the token.
type MfaVerifyHandler struct {
	usecases domain.AuthUseCases
	cookie   CookieConfig
}

func NewMfaVerifyHandler(uc domain.AuthUseCases, cookie CookieConfig) *MfaVerifyHandler {
	return &MfaVerifyHandler{usecases: uc, cookie: cookie}
}

func (h *MfaVerifyHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/mfa/verify",
		Exposure: types.Exposures.External,
		Tx:       true,
	}
}

func (h *MfaVerifyHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.MfaCodeBody{}}
}

func (h *MfaVerifyHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.MfaCodeBody)

	res, err := h.usecases.MfaVerify(r.Context(), h.cookie.token(r), body.Code)
	if err != nil {
		return nil, err
	}

	h.cookie.set(w, res.SessionToken) // rotated token
	return httpkit.Ok(dto.LoginResultToJSON(res)), nil
}
