package handlers

import (
	"net/http"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

type CheckHandler struct {
	mode types.ServerMode
}

func NewCheckHandler(mode types.ServerMode) *CheckHandler {
	return &CheckHandler{mode: mode}
}

func (h *CheckHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodGet,
		Path:     "/",
		Exposure: types.Exposures.Both,
	}
}

func (h *CheckHandler) Execute(w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester) (*types.HttpResponse, error) {
	return httpkit.Ok(&types.HealthCheckResponse{
		Status:    200,
		Mode:      h.mode,
		Timestamp: time.Now().UTC(),
	}), nil
}
