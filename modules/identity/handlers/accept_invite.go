package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/identity/dto"
)

// AcceptInviteHandler is PUBLIC (no session, no tenant): the invited person
// redeems the "zti_" token with a password and becomes active. No Tx flag
// either — the use case opens its own transaction bound to the tenant taken
// from the token row (a route-level tx would be tenant-less, so RLS-scoped
// writes like the audit entry would fail closed).
type AcceptInviteHandler struct {
	usecases domain.MemberUseCases
}

func NewAcceptInviteHandler(uc domain.MemberUseCases) *AcceptInviteHandler {
	return &AcceptInviteHandler{usecases: uc}
}

func (h *AcceptInviteHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/accept-invite",
		Exposure: types.Exposures.External,
	}
}

func (h *AcceptInviteHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.AcceptInviteBody{}}
}

func (h *AcceptInviteHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.AcceptInviteBody)
	res, err := h.usecases.AcceptInvite(r.Context(), domain.AcceptInviteInput{
		Token: body.Token, Password: body.Password, Name: body.Name,
	})
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(map[string]any{"email": res.Email}), nil
}
