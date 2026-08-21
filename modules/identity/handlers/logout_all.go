package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
)

// LogoutAllHandler revokes every session for the current user (full session).
type LogoutAllHandler struct {
	usecases domain.AuthUseCases
	cookie   CookieConfig
}

func NewLogoutAllHandler(uc domain.AuthUseCases, cookie CookieConfig) *LogoutAllHandler {
	return &LogoutAllHandler{usecases: uc, cookie: cookie}
}

func (h *LogoutAllHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/logout-all",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *LogoutAllHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	if err := h.usecases.LogoutAll(r.Context(), requester.ID); err != nil {
		return nil, err
	}
	h.cookie.clear(w)
	return httpkit.NoContent(), nil
}
