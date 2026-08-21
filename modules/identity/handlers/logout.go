package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
)

// LogoutHandler revokes the current session. Public (reads its own cookie) so it
// works even for an mfa_pending session.
type LogoutHandler struct {
	usecases domain.AuthUseCases
	cookie   CookieConfig
}

func NewLogoutHandler(uc domain.AuthUseCases, cookie CookieConfig) *LogoutHandler {
	return &LogoutHandler{usecases: uc, cookie: cookie}
}

func (h *LogoutHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/logout",
		Exposure: types.Exposures.External,
		Tx:       true,
	}
}

func (h *LogoutHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	if err := h.usecases.Logout(r.Context(), h.cookie.token(r)); err != nil {
		return nil, err
	}
	h.cookie.clear(w)
	return httpkit.NoContent(), nil
}
