package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/identity/dto"
)

type LoginHandler struct {
	usecases domain.AuthUseCases
	cookie   CookieConfig
}

func NewLoginHandler(uc domain.AuthUseCases, cookie CookieConfig) *LoginHandler {
	return &LoginHandler{usecases: uc, cookie: cookie}
}

func (h *LoginHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/login",
		Exposure: types.Exposures.External,
		Tx:       true, // transactional, but no tenant/session (public)
	}
}

func (h *LoginHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.LoginBody{}}
}

func (h *LoginHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.LoginBody)

	ip := clientIP(r)
	ua := r.UserAgent()
	res, err := h.usecases.Login(r.Context(), domain.LoginInput{
		Email: body.Email, Password: body.Password, IP: &ip, UserAgent: &ua,
	})
	if err != nil {
		return nil, err
	}

	h.cookie.set(w, res.SessionToken)
	return httpkit.Ok(dto.LoginResultToJSON(res)), nil
}
