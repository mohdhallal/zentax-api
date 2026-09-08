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

// Member administration under /members. Reads (the tenant directory) are open
// to every role via member:read; every mutation is tenant-admin surface gated
// by member:manage. The tenant always comes from the requester's session.

// --- GET /members ---

type ListMembersHandler struct {
	usecases domain.MemberUseCases
}

func NewListMembersHandler(uc domain.MemberUseCases) *ListMembersHandler {
	return &ListMembersHandler{usecases: uc}
}

func (h *ListMembersHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberRead,
		Paginated:  true,
	}
}

func (h *ListMembersHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Query: dto.ListMembersQuery{}}
}

func (h *ListMembersHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	q, _ := input.Query.(*dto.ListMembersQuery)
	kind, status := "", ""
	if q.Kind != nil {
		kind = *q.Kind
	}
	if q.Status != nil {
		status = *q.Status
	}
	members, total, err := h.usecases.ListMembers(r.Context(), kind, status, q.Search, q.Limit, q.Offset)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(members))
	for i := range members {
		out = append(out, dto.MemberToJSON(&members[i]))
	}
	return httpkit.Ok(out).WithPagination(types.Pagination{Total: total, Limit: q.Limit, Offset: q.Offset}), nil
}

// --- GET /members/{id} ---

type GetMemberHandler struct {
	usecases domain.MemberUseCases
}

func NewGetMemberHandler(uc domain.MemberUseCases) *GetMemberHandler {
	return &GetMemberHandler{usecases: uc}
}

func (h *GetMemberHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberRead,
	}
}

func (h *GetMemberHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Params: dto.MemberIdParams{}}
}

func (h *GetMemberHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.MemberIdParams)
	m, err := h.usecases.GetMember(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.MemberToJSON(m)), nil
}

// --- POST /members (invite) ---

type InviteMemberHandler struct {
	usecases domain.MemberUseCases
}

func NewInviteMemberHandler(uc domain.MemberUseCases) *InviteMemberHandler {
	return &InviteMemberHandler{usecases: uc}
}

func (h *InviteMemberHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *InviteMemberHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.CreateMemberBody{}}
}

func (h *InviteMemberHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.CreateMemberBody)
	res, err := h.usecases.Invite(r.Context(), domain.CreateMemberInput{
		Email: body.Email, Name: body.Name, Role: body.Role, ScopeEntityID: body.ScopeEntityID,
	})
	if err != nil {
		return nil, err
	}
	return httpkit.Created(dto.InviteToJSON(res, true)), nil
}

// --- POST /members/{id}/invite (re-issue) ---

type ReissueInviteHandler struct {
	usecases domain.MemberUseCases
}

func NewReissueInviteHandler(uc domain.MemberUseCases) *ReissueInviteHandler {
	return &ReissueInviteHandler{usecases: uc}
}

func (h *ReissueInviteHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/{id}/invite",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *ReissueInviteHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Params: dto.MemberIdParams{}}
}

func (h *ReissueInviteHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.MemberIdParams)
	res, err := h.usecases.ReissueInvite(r.Context(), params.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Created(dto.InviteToJSON(res, false)), nil
}

// --- PUT /members/{id} ---

type UpdateMemberHandler struct {
	usecases domain.MemberUseCases
}

func NewUpdateMemberHandler(uc domain.MemberUseCases) *UpdateMemberHandler {
	return &UpdateMemberHandler{usecases: uc}
}

func (h *UpdateMemberHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPut,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *UpdateMemberHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.UpdateMemberBody{}, Params: dto.MemberIdParams{}}
}

func (h *UpdateMemberHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.UpdateMemberBody)
	params, _ := input.Params.(*dto.MemberIdParams)
	m, err := h.usecases.UpdateMember(r.Context(), params.ID, domain.UpdateMemberInput{
		Name: body.Name, Status: body.Status,
	})
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.MemberToJSON(m)), nil
}

// --- PUT /members/{id}/role (replace all grants) ---

type SetMemberRoleHandler struct {
	usecases domain.MemberUseCases
}

func NewSetMemberRoleHandler(uc domain.MemberUseCases) *SetMemberRoleHandler {
	return &SetMemberRoleHandler{usecases: uc}
}

func (h *SetMemberRoleHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPut,
		Path:       "/{id}/role",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *SetMemberRoleHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.GrantBody{}, Params: dto.MemberIdParams{}}
}

func (h *SetMemberRoleHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.GrantBody)
	params, _ := input.Params.(*dto.MemberIdParams)
	m, err := h.usecases.SetRole(r.Context(), params.ID, domain.GrantInput{Role: body.Role, ScopeEntityID: body.ScopeEntityID})
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.MemberToJSON(m)), nil
}

// --- POST /members/{id}/grants (add one) ---

type AddMemberGrantHandler struct {
	usecases domain.MemberUseCases
}

func NewAddMemberGrantHandler(uc domain.MemberUseCases) *AddMemberGrantHandler {
	return &AddMemberGrantHandler{usecases: uc}
}

func (h *AddMemberGrantHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/{id}/grants",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *AddMemberGrantHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.GrantBody{}, Params: dto.MemberIdParams{}}
}

func (h *AddMemberGrantHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.GrantBody)
	params, _ := input.Params.(*dto.MemberIdParams)
	m, err := h.usecases.AddGrant(r.Context(), params.ID, domain.GrantInput{Role: body.Role, ScopeEntityID: body.ScopeEntityID})
	if err != nil {
		return nil, err
	}
	return httpkit.Created(dto.MemberToJSON(m)), nil
}

// --- DELETE /members/{id}/grants/{grantId} ---

type RemoveMemberGrantHandler struct {
	usecases domain.MemberUseCases
}

func NewRemoveMemberGrantHandler(uc domain.MemberUseCases) *RemoveMemberGrantHandler {
	return &RemoveMemberGrantHandler{usecases: uc}
}

func (h *RemoveMemberGrantHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodDelete,
		Path:       "/{id}/grants/{grantId}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.MemberManage,
	}
}

func (h *RemoveMemberGrantHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Params: dto.MemberGrantParams{}}
}

func (h *RemoveMemberGrantHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.MemberGrantParams)
	if err := h.usecases.RemoveGrant(r.Context(), params.ID, params.GrantID); err != nil {
		return nil, err
	}
	return httpkit.NoContent(), nil
}
