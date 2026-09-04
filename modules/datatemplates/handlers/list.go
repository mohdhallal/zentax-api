package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// ListDataTemplatesHandler handles GET /data-templates — not paginated (a
// tenant's template registry is small), sorted by name, filterable by
// templateType / category.
type ListDataTemplatesHandler struct {
	usecases domain.DataTemplateUseCases
}

func NewListDataTemplatesHandler(uc domain.DataTemplateUseCases) *ListDataTemplatesHandler {
	return &ListDataTemplatesHandler{usecases: uc}
}

func (h *ListDataTemplatesHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DataTemplateRead,
	}
}

func (h *ListDataTemplatesHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListDataTemplatesQuery{},
	}
}

func (h *ListDataTemplatesHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	query, _ := input.Query.(*dto.ListDataTemplatesQuery)

	items, err := h.usecases.List(r.Context(), domain.ListDataTemplatesArgs{
		TemplateType: query.TemplateType,
		Category:     query.Category,
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.DataTemplatesToJSON(items)), nil
}
