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
//
// It declares NO TENANT and NO TRANSACTION, and reads the session cookie itself
// — the same shape as MfaVerifyHandler, and for the same reason. Confirmation
// guesses are charged against a per-session attempt budget before the code is
// validated, and `Tenant` implies a request transaction that the middleware
// rolls back on any status >= 400, which would discard the charge with the 401
// it answers. The use case re-performs exactly what RequireAuth performed (live
// session, active user, MFA not still pending); the one behavioural change is
// that a bearer API token no longer reaches this route, which is correct — a
// service account has no authenticator app. CSRF is unaffected: the origin guard
// wraps every external route regardless of tenancy.
type MfaEnableHandler struct {
	usecases domain.AuthUseCases
	cookie   CookieConfig
}

func NewMfaEnableHandler(uc domain.AuthUseCases, cookie CookieConfig) *MfaEnableHandler {
	return &MfaEnableHandler{usecases: uc, cookie: cookie}
}

func (h *MfaEnableHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/mfa/enable",
		Exposure: types.Exposures.External,
	}
}

func (h *MfaEnableHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.MfaCodeBody{}}
}

func (h *MfaEnableHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.MfaCodeBody)

	if err := h.usecases.MfaEnable(r.Context(), h.cookie.token(r), body.Code); err != nil {
		return nil, err
	}
	return httpkit.NoContent(), nil
}
