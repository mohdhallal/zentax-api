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

// SeedPredefinedHandler handles POST /data-templates/predefined: idempotently
// inserts the predefined templates the tenant is missing and returns the full
// predefined list (200 — nothing is necessarily created).
type SeedPredefinedHandler struct {
	usecases domain.DataTemplateUseCases
}

func NewSeedPredefinedHandler(uc domain.DataTemplateUseCases) *SeedPredefinedHandler {
	return &SeedPredefinedHandler{usecases: uc}
}

func (h *SeedPredefinedHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/predefined",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.DataTemplateWrite,
	}
}

func (h *SeedPredefinedHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{}
}

func (h *SeedPredefinedHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	items, err := h.usecases.SeedPredefined(r.Context())
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.DataTemplatesToJSON(items)), nil
}
