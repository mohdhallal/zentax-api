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

// Account settings under /tenant (ADR-0003 / ADR-0023 §6). The tenant is the
// requester's own — there is no id in the path and none is accepted. Reads
// are open to every role (member:read); the write is tenant-admin surface
// (member:manage).

// --- GET /tenant ---

type GetTenantHandler struct {
	usecases domain.TenantUseCases
}

func NewGetTenantHandler(uc domain.TenantUseCases) *GetTenantHandler {
	return &GetTenantHandler{usecases: uc}
}

func (h *GetTenantHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberRead,
	}
}

func (h *GetTenantHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	t, err := h.usecases.GetTenant(r.Context())
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.TenantToJSON(t)), nil
}

// --- PUT /tenant ---

type UpdateTenantHandler struct {
	usecases domain.TenantUseCases
}

func NewUpdateTenantHandler(uc domain.TenantUseCases) *UpdateTenantHandler {
	return &UpdateTenantHandler{usecases: uc}
}

func (h *UpdateTenantHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPut,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *UpdateTenantHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.UpdateTenantBody{}}
}

func (h *UpdateTenantHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.UpdateTenantBody)
	t, err := h.usecases.UpdateTenant(r.Context(), domain.UpdateTenantInput{Name: body.Name, Timezone: body.Timezone})
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.TenantToJSON(t)), nil
}
