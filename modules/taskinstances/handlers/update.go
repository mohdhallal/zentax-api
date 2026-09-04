package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

type UpdateTaskInstanceHandler struct {
	usecases domain.TaskInstanceUseCases
}

func NewUpdateTaskInstanceHandler(uc domain.TaskInstanceUseCases) *UpdateTaskInstanceHandler {
	return &UpdateTaskInstanceHandler{usecases: uc}
}

func (h *UpdateTaskInstanceHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPut,
		Path:       "/{id}",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.TaskWrite,
	}
}

func (h *UpdateTaskInstanceHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body:   dto.UpdateTaskInstanceBody{},
		Params: dto.TaskInstanceIdParams{},
	}
}

func (h *UpdateTaskInstanceHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.UpdateTaskInstanceBody)
	params, _ := input.Params.(*dto.TaskInstanceIdParams)

	var dueDate *dateonly.Date
	if body.DueDate != nil {
		parsed, err := dateonly.Parse(*body.DueDate)
		if err != nil {
			return nil, apperrors.NewValidation("dueDate must be a YYYY-MM-DD date")
		}
		dueDate = &parsed
	}

	ti, err := h.usecases.Update(r.Context(), params.ID, domain.UpdateTaskInstanceInput{
		Status:         body.Status,
		AssigneeID:     body.AssigneeID,
		DueDate:        dueDate,
		Notes:          body.Notes,
		DataTemplateID: body.DataTemplateID,
		TaxData:        body.TaxData,
		TaxDataStatus:  body.TaxDataStatus,
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.TaskInstanceToJSON(ti)), nil
}
