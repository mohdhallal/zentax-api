package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/identity/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Service-account + API-token management (machine identity, agentic-AI B1).
// Everything here is tenant-admin surface: gated by member:manage.

type CreateServiceAccountHandler struct {
	usecases domain.ServiceAccountUseCases
}

func NewCreateServiceAccountHandler(uc domain.ServiceAccountUseCases) *CreateServiceAccountHandler {
	return &CreateServiceAccountHandler{usecases: uc}
}

func (h *CreateServiceAccountHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *CreateServiceAccountHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.CreateServiceAccountBody{}}
}

func (h *CreateServiceAccountHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.CreateServiceAccountBody)
	sa, err := h.usecases.CreateServiceAccount(r.Context(), domain.CreateServiceAccountInput{
		Name: body.Name, Role: body.Role, ScopeEntityID: body.ScopeEntityID,
	})
	if err != nil {
		return nil, err
	}
	return httpkit.Created(dto.ServiceAccountToJSON(sa)), nil
}

type ListServiceAccountsHandler struct {
	usecases domain.ServiceAccountUseCases
}

func NewListServiceAccountsHandler(uc domain.ServiceAccountUseCases) *ListServiceAccountsHandler {
	return &ListServiceAccountsHandler{usecases: uc}
}

func (h *ListServiceAccountsHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *ListServiceAccountsHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	accounts, err := h.usecases.ListServiceAccounts(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(accounts))
	for i := range accounts {
		out = append(out, dto.ServiceAccountToJSON(&accounts[i]))
	}
	return httpkit.Ok(out), nil
}

type IssueTokenHandler struct {
	usecases domain.ServiceAccountUseCases
}

func NewIssueTokenHandler(uc domain.ServiceAccountUseCases) *IssueTokenHandler {
	return &IssueTokenHandler{usecases: uc}
}

func (h *IssueTokenHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/{id}/tokens",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *IssueTokenHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body:   dto.IssueTokenBody{},
		Params: dto.ServiceAccountIdParams{},
	}
}

func (h *IssueTokenHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.IssueTokenBody)
	params, _ := input.Params.(*dto.ServiceAccountIdParams)

	res, err := h.usecases.IssueToken(r.Context(), params.ID, body.Label, body.ExpiresInDays)
	if err != nil {
		return nil, err
	}
	return httpkit.Created(dto.IssuedTokenToJSON(res)), nil
}

type RevokeTokenHandler struct {
	usecases domain.ServiceAccountUseCases
}

func NewRevokeTokenHandler(uc domain.ServiceAccountUseCases) *RevokeTokenHandler {
	return &RevokeTokenHandler{usecases: uc}
}

func (h *RevokeTokenHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/{id}/revoke",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *RevokeTokenHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Params: dto.TokenIdParams{}}
}

func (h *RevokeTokenHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.TokenIdParams)
	if err := h.usecases.RevokeToken(r.Context(), params.ID); err != nil {
		return nil, err
	}
	return httpkit.NoContent(), nil
}
